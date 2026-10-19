# ADR 0011: Actors, reserved CLI names and package layout

**Requirements:** R7, R18

## Status

Accepted

## Context

The M1 platform core design
(`docs/superpowers/specs/2026-10-17-m1-platform-core-design.md`) needs two
audit actors that the shared contracts do not define, top-level CLI commands
for core actions that ADR 0006's reserved names do not cover, and packages
that ADR 0001's layout does not name. Accepted ADRs are not edited, so the
additions are recorded here.

## Decision

- **Actors.** Two actors join the shared contracts' list:
  - `anonymous`: a failed login whose username matched no user. The audit
    row's input holds the submitted username.
  - `local:uid-<n>@<host id>`: a caller on the controller socket whose uid is
    not 0, accepted only in `--dev` mode.
- **Reserved CLI names.** `host`, `module`, `token`, `audit`, `help` and
  `completion` join ADR 0006's reserved top-level names. Core actions (module
  id `core`) are exposed without the module segment, as
  `darkpanel <noun> <verb>`, so their nouns must not collide with a module id;
  `help` and `completion` are commands cobra adds itself. A module id that is
  a reserved name stops startup.
- **Package layout.** Additions to ADR 0001's layout:

```
internal/core/store/            SQLite, migrations and backups for both roles
internal/core/coretest/         fakes of the core interfaces for tests
internal/paths/                 file and directory locations of both roles
internal/buildinfo/             version and commit stamped by the build
internal/registry/              builds the one module registry
internal/cli/                   the CLI generated from the registry
internal/controller/api/        HTTP API and controller socket
internal/controller/auth/       users, passwords, TOTP, sessions, tokens
internal/controller/listen/     listeners and the web authority (ADR 0010)
internal/controller/dispatch/   routes actions to the controller or an agent
internal/controller/audit/      audit log and agent journal ingestion
internal/controller/coreactions/ the core module's actions
internal/controller/server/     startup order of the controller
internal/agent/runtime/         the agent half: database, engine, preflight
test/harness/                   VM integration harness (ADR 0008)
```

- **Moves** relative to ADR 0001: the database code is
  `internal/core/store`, shared by both roles, instead of a controller-only
  package; the audit log is `internal/controller/audit`, while the agent
  journal stays with the change lifecycle under `internal/core`; the web
  authority lives in `internal/controller/listen`.

## Alternatives considered

- Editing ADR 0001 and ADR 0006: rejected; they are Accepted, and the index's
  lifecycle requires a new ADR for a change.
- Recording a failed login for an unknown username with actor
  `user:<submitted name>`: rejected; it would mix user ids and names in one
  field and look like a real user.
- A `core` segment on core commands (`darkpanel core token create`):
  rejected; ADR 0006 already reserves `user` and `change` as top-level
  commands, and the core's other nouns would be inconsistent with them.

## Consequences

- The shared contracts' actor list in the index points to this ADR for the
  two additions.
- A module can never take a reserved name as its id; the registry refuses it.
- Later epics place files by ADR 0001 together with this layout.

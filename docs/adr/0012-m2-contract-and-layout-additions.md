# ADR 0012: M2 contract and layout additions

**Requirements:** R22, R23, R24, R25, R26, R27, R28, R29, R30, R31, R32, R33, R34, R35, R36, R80

## Status

Proposed

## Context

The M2 fleet and lifecycle design
(`docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`) makes
concrete several contracts that ADR 0002, ADR 0003, ADR 0004 and ADR 0007
state in outline, closes the index's deferred decision on how the install
script verifies a download, and adds packages that ADR 0001 and ADR 0011 do
not name. Accepted ADRs are not edited, so the additions are recorded here.

## Decision

- **Install script verification.** `install.sh` verifies a release with
  OpenSSL 3 alone, against the trusted public keys embedded in the script when
  it is generated from `install.sh.in`; it downloads nothing but the release
  files. It hashes `checksums.txt` with BLAKE2b-512, checks one minisign
  hashed-mode (`ED`) signature over that digest and the global signature over
  the signature and its trusted comment with `openssl pkeyutl -verify -rawin`,
  requires the trusted comment
  `version=<version> channel=<stable|beta> purpose=checksums` for the version
  being installed, then checks the tarball against `checksums.txt`. Any one
  valid signature from an embedded key suffices.
- **Signature files and release index.** Each signing key produces
  `checksums.txt.<key id>.minisig`, the key id written as 16 upper-case
  hexadecimal digits. A missing or failing signature file is one unsuccessful
  candidate, not an error. The rolling release `index` holds
  `releases.json`, `{"formatVersion": 1, "releases": [{"version", "channel"},
  …]}`, regenerated from every published release on each publication; it only
  selects versions, and trust comes from each version's own signatures.
- **Agent routes.** The agent API is the concrete form of ADR 0003's endpoint
  list. Under `/agent/v1/`: `GET hello`; `POST actions/{name}/{version}/read`;
  `GET resources/{module}/{resource}`; `POST actions/{name}/{version}/plan`;
  `POST plans/{id}/apply`; `POST changes/{id}/confirm`;
  `POST changes/{id}/revert`; `GET changes/{id}`; `GET changes` with the
  change filter as query parameters; `GET modules/{id}/preflight`;
  `GET journal?after=&limit=`; `POST journal/ack`. Bodies are explicit wire
  types with camelCase fields, UTC RFC 3339 times, durations in integer
  seconds and nullable fields as JSON `null`, limited to 4 MiB; errors are RFC
  9457 problem details. Frozen for protocol version 1: `hello`, the plan,
  apply and read routes of `core.update.apply/1`, `core.update.rollback/1` and
  `core.update.status/1`, the yamux framing and the headers below.
- **Headers.** The controller sends `Darkpanel-Principal` (the JSON of the
  calling principal) on every request, and on `GET hello` also
  `Darkpanel-Protocol`, `Darkpanel-Version` and `Darkpanel-Controller-Id`, so
  the hello runs both ways. The agent accepts `Darkpanel-Principal` only from
  the authenticated controller; on its local socket the principal is always
  `local:root@<host id>`.
- **Schema hashes.** A schema hash is the hex SHA-256 of a snapshot's `input`
  or `output` member re-encoded with Go's `encoding/json` `Marshal` after
  decoding it into `any`. Hello reports one input and output hash per
  agent-side action version, keyed `<name>@<version>`. The binary embeds
  `schemas/`, including released snapshots. Per action version the
  dispatcher sends as is when the hash equals its own; when the hash matches
  a released snapshot it validates the input against that older schema with
  unknown properties refused, then sends; an unknown hash makes the action
  version unavailable on that host.
- **Journal epoch.** The agent stores a ULID `journal_epoch`, replaced only
  when its database or journal table is recreated and kept across
  re-enrollment. The controller's journal cursor and audit rows carry the
  epoch; a new epoch restarts the host's cursor at zero, and the audit's
  uniqueness is `(host, epoch, seq)`.
- **Operation reservation.** `lifecycle.Reserve` takes the lock key `*` for
  an owner `op:<operation id>` in its own write transaction, refused while any
  lock is held or a revertible change is unconfirmed; while `*` is held, every
  other lock is refused. `lifecycle.Release` frees an owner's rows. The engine
  passes the running change's id to Apply and Verify as `Env.ChangeID`.
- **Update actions.** `core.update.apply/v1` (compensable, compensated by
  `core.update.rollback`), `core.update.rollback/v1` (irreversible) and
  `core.update.status/v1` (read) are agent-side core actions. Their inputs
  carry no URL or key: the agent builds every download URL from its embedded
  base and enforces the downgrade and one-minor stepping rules itself. Apply
  reserves `*`, records the transaction and starts a transient systemd unit
  that runs the update, then returns before any restart; the result is read
  through `core.update.status`, never through the change.
- **Update and rollout shapes.** The Go types are in `internal/core/fleet.go`;
  JSON fields are:
  - `core.update.apply/v1`: input `UpdateApplyInput` {`version`, `channel`,
    `force`}, output `UpdateTransaction`.
  - `core.update.rollback/v1`: input `UpdateRollbackInput` {`force`}, output
    `UpdateTransaction`.
  - `core.update.status/v1`: input `UpdateStatusInput` {`operationId`,
    `changeId`, both optional, at most one}, output `UpdateStatus`
    {`transactions`}.
  - `UpdateTransaction` is {`id`, `operationId`, `parentId`, `kind` (`update`,
    `rollback`), `fromVersion`, `toVersion`, `targetVersion`, `state`
    (`pending`, `downloading`, `staged`, `swapped`, `rolling-back`,
    `committed`, `rolled-back`, `failed`), `error` (`ErrorDetail` {`code`,
    `message`} or null), `startedAt`, `updatedAt`}.
  - `core.host.update-policy/v1`: input and output `UpdatePolicy` {`hostId`,
    `channel`, `schedule` (null or `UpdateSchedule` {`days`, `time`})}.
  - `core.update.rollout/v1`: input `RolloutInput` {`hosts`, `version`,
    `channel`}, output `Rollout`; `core.update.rollout-cancel/v1`: input
    `RolloutCancelInput` {`rolloutId`, `force`}, output `Rollout`;
    `core.update.rollouts/v1`: input `RolloutsInput` {`limit`}, output
    `RolloutList` {`rollouts`}.
  - `Rollout` is {`id`, `version`, `channel`, `state` (`running`, `draining`,
    `succeeded`, `failed`, `cancelled`), `error`, `createdAt`, `updatedAt`,
    `hosts`}, and each `RolloutHost` is {`hostId`, `position`, `state`
    (`waiting`, `updating`, `committed`, `failed`, `skipped`), `planId`,
    `operationId`, `error`}.
- **Update lock and rollback claim.** Every change to a transaction record
  and every rename in the binary directory happens under an exclusive `flock`
  on `update/lock`. A recover path that cannot take the lock within 5 s kills
  the runner's unit and takes it. A rollback first claims the record
  `swapped → rolling-back` under the lock; only the claimant proceeds.
- **Recovery executable.** ADR 0007's `darkpanel.prev update rollback
  --pending` takes the form `darkpanel.recovery update recover`:
  `darkpanel.recovery` is a hard link to the previous binary, made before the
  swap and removed when the operation is terminal, so a crash at any point
  leaves a binary that can finish the rollback. The rollback and boot
  recovery units run it.
- **Health rule.** After a swap the runner polls every 2 s until an 80 s
  deadline. The controller passes when `/healthz` on its socket reports the
  new version with status `ok`, and fails at once on the new version with any
  other status; the agent passes when its hello reports the new version and,
  when enrolled, a live controller connection. A refused connection, a timeout
  or the old version is "not yet"; the deadline is a failure.
- **Actors and reserved names.** `system:rollout` (the rollout driver),
  `system:update-schedule` (scheduled updates) and `local:root@unenrolled`
  (the agent socket before enrollment) join the shared contracts' actors.
  `system` joins the reserved top-level CLI names.
- **Dependency.** `github.com/hashicorp/yamux` (MPL-2.0, file-level copyleft)
  is accepted, used unmodified, keeping ADR 0003's frozen yamux framing.
- **Package layout.** Additions to ADR 0001 and ADR 0011:

```
internal/transport/             TLS policies, peer checks, yamux session, heartbeat
internal/agent/api/             agent API handler and client
internal/agent/wire/            agent API wire types
internal/agent/daemon/          dial loop, enrollment, renewal, credentials
internal/agent/socket/          the agent's local socket
internal/controller/fleet/      agent authority, enrollment, session registry
internal/controller/rollout/    fleet rollouts and the update scheduler
internal/install/               system install, units and uninstall
internal/update/                release verification, staging, swap, rollback
internal/update/trustedkeys/    trusted release keys and the release base URL
internal/platform/systemd.go    systemd over D-Bus
schemas/embed.go                the embedded schema snapshots
```

## Alternatives considered

- A pinned minisign binary or a required `minisign` package for the install
  script: rejected; OpenSSL 3 ships on every supported operating system.
- Sending only schema hashes and marking every mismatch unavailable:
  rejected; each additive field would break the previous-minor window (R25).
- Modelling an update as a change that completes after the restart: rejected;
  startup recovery would mark it interrupted.
- The GitHub API as the release source: rejected; unauthenticated rate
  limits and no harness equivalent.
- `xtaci/smux` (MIT) instead of yamux: rejected; ADR 0003 freezes yamux
  framing.

## Consequences

- The index's deferred decision on install-script verification is closed.
- The shared contracts' actor list in the index points to this ADR for the
  three additions.
- The frozen protocol-1 surface is the route table and headers above, so a
  change to them needs protocol version 2.
- Later epics place files by ADR 0001, ADR 0011 and this layout.

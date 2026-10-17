# ADR 0007: Release, signing, update and rollback

**Requirements:** R1, R26, R29, R31, R32, R33, R34, R35, R36

## Status

Accepted

## Context

Success measure S5 requires that upgrades never break a host and that a failed
upgrade rolls back automatically within two minutes. On 2026-10-17 the owner
chose to sign releases in CI, in an approval-gated environment.

The review findings that shape this decision:

- A new binary that cannot start cannot roll itself back.
- A compromised build would be signed. The Webmin 1.890-1.920 backdoor came
  from the build server.
- Key rotation stranded hosts that skip releases.
- A replayed older signed file allowed downgrades.
- Upgrade ordering was undefined.

The owner review on 2026-10-17 added more gaps: the unprivileged controller
had no path to a root process that swaps the binary, a reboot dropped the
transient health-check unit, a host skipping several minor releases could not
roll back, and the rollback binary could not read records a newer binary
wrote.

## Decision

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

## Alternatives considered

- The owner signs locally: rejected by the owner on 2026-10-17 in favour of
  the gated environment; manual and slower.
- Sigstore keyless signing: rejected; a much larger verification dependency
  and reliance on Sigstore services.
- The service updating itself in-process: rejected; a crash during the swap
  leaves no process to roll back.
- The controller running the update: rejected; it is unprivileged and cannot
  swap binaries or restart units.
- Updating across several minor releases in one transaction: rejected; the
  previous binary could not run on a schema contracted two releases later.
- Writing minisign verification ourselves: rejected; the format is stable and
  `aead.dev/minisign` is small and pinned.

## Consequences

- An update is refused, not queued, while a revertible change is unconfirmed.
- The build workflow pins actions by commit SHA.
- A key rotation spans at least two minor releases.
- The M2 self-update epic implements the update action, the transaction
  record, the rollback and boot-recovery units, stepping and resumable
  rollouts.
- A reboot during an update's health-check window rolls the update back.
- The M2 install epic decides the install script's verification method.

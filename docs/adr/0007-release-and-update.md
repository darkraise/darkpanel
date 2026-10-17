# ADR 0007: Release, signing, update and rollback

**Requirements:** R1, R29, R31, R32, R33, R34, R35, R36

## Status

Proposed

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

## Alternatives considered

- The owner signs locally: rejected by the owner on 2026-10-17 in favour of
  the gated environment; manual and slower.
- Sigstore keyless signing: rejected; a much larger verification dependency
  and reliance on Sigstore services.
- The service updating itself in-process: rejected; a crash during the swap
  leaves no process to roll back.
- Writing minisign verification ourselves: rejected; the format is stable and
  `aead.dev/minisign` is small and pinned.

## Consequences

- An update is refused, not queued, while a revertible change is unconfirmed.
- The build workflow pins actions by commit SHA.
- A key rotation spans at least two minor releases.
- The M2 self-update epic implements the transaction, the rollback unit and
  resumable rollouts.
- The M2 install epic decides the install script's verification method.

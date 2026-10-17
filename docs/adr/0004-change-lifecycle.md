# ADR 0004: Change lifecycle, locking and recovery

**Requirements:** R1, R10, R18, R28, R39, R40, R41, R42

## Status

Proposed

## Context

Success measure S1 requires that no firewall change can lock the owner out.
Requirement R10, as amended on 2026-10-17, divides actions into revertible,
compensable and irreversible classes; see the shared contracts in the index.

The review findings 3, 8, 9, 10 and 24 showed these gaps:

- A crash between the mutation and the persisting of the revert deadline
  caused a permanent lockout.
- A lost apply response could make the same change act twice.
- Per-module locks missed resources shared between modules.
- Controller-side actions had no lifecycle.
- Snapshot formats were unversioned.

## Decision

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

## Alternatives considered

- Revert timer in the browser or the controller: rejected. A lost connection or
  a controller restart would leave an unconfirmed change in place.
- Hashing the whole host state for plans: rejected. Unrelated changes, such as
  a recreated container, would refuse valid plans.
- One global lock per host: rejected. It serializes unrelated modules for no
  safety gain.

## Consequences

- Every mutating action declares a class and its lock keys.
- Agents must fsync before mutating.
- Agent startup runs recovery before serving requests.
- The M3 lockout guard plugs into plan guards.
- Controller-side actions need snapshots where the remote API allows.
- Integration tests kill the agent mid-apply (ADR 0008).

# ADR 0004: Change lifecycle, locking and recovery

**Requirements:** R1, R10, R18, R28, R39, R40, R41, R42

## Status

Accepted

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

## Alternatives considered

- Revert timer in the browser or the controller: rejected. A lost connection or
  a controller restart would leave an unconfirmed change in place.
- Hashing the whole host state for plans: rejected. Unrelated changes, such as
  a recreated container, would refuse valid plans.
- One global lock per host: rejected. It serializes unrelated modules for no
  safety gain.

## Consequences

- Every mutating action declares a class and its lock keys.
- Agents must fsync before mutating, and locks live in the database.
- A crash during a compensable or irreversible action is reported, never
  guessed at; an operator decides the next step.
- Agent startup runs recovery before serving requests.
- The M3 lockout guard plugs into plan guards.
- Controller-side actions need snapshots where the remote API allows.
- Integration tests kill the agent mid-apply (ADR 0008).

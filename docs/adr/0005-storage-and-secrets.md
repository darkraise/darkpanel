# ADR 0005: Storage, migrations, secrets and keys

**Requirements:** R1, R8, R12, R34, R81

## Status

Proposed

## Context

The Darkpanel binary is static and built with CGO disabled, so the storage
layer cannot depend on a C library. Updates must never break a production host
(S5), which puts hard demands on how the schema changes and how data is kept
across an update and a rollback.

Review findings 2, 12, 19, 20 and 33 shaped this decision:

- Restoring a backup on rollback erased pending reverts and enrollments that
  were recorded after the backup was taken.
- The secret key had no lifecycle.
- The certificate authority key had no custody.
- A failed module migration could stop all startup.
- The SQLite driver has connection caveats that must be handled explicitly.

## Decision

- SQLite through `modernc.org/sqlite` (exact versions pinned, including
  `modernc.org/libc`), write-ahead-log mode, pragmas set in the connection
  string, one writer connection: `/var/lib/darkpanel/controller.db` (owned by
  `darkpanel`) and `/var/lib/darkpanel/agent.db` (root).
- Core tables use the prefix `core_`; each module uses `<module id>_`.
- Migrations: embedded SQL per module and role, numbered, forward-only, each in
  a transaction, recorded in `core_migrations` with a checksum. A changed
  checksum or failed core migration stops startup; a failed module migration
  marks only that module unavailable (ADR 0002).
- **Expand, then contract.** A release's migrations must leave the schema usable
  by the previous minor release's binary. Destructive changes (drop, rename)
  ship only in a later release, after nothing reads the old shape. A binary
  rollback therefore runs the old binary on the new schema without restoring
  data.
- Before migrating, the database is copied with `VACUUM INTO` to
  `/var/lib/darkpanel/backups/<update id>/` (mode 0600, last five kept), for
  disaster recovery only. The agent's journal and revert records are never
  restored from a backup.
- **Secrets.** XChaCha20-Poly1305 with a random 24-byte nonce and associated
  data `<table>.<column>.<row id>`; ciphertexts carry a key id so rotation can
  be added. Each role has its own key, created atomically at install
  (`/etc/darkpanel/controller.key` owned by `darkpanel`,
  `/etc/darkpanel/agent.key` owned by root, both 0600). Agents hold only the
  secrets their own modules use (for example Nginx Proxy Manager credentials
  on its host).
- **Certificate authority key** is stored encrypted in `controller.db`.
  `darkpanel controller backup` exports the database and the controller key
  into one encrypted archive; `restore` is the lost-controller path in the
  runbook (R81).

## Alternatives considered

- `mattn/go-sqlite3`: rejected; needs CGO.
- PostgreSQL: rejected; an extra service on every controller host for a small
  data set.
- Down migrations: rejected; rarely tested, and expand-then-contract makes a
  binary rollback safe without them.
- Restoring the database on update rollback: rejected; see Context.
- One shared secret key for controller and agent: rejected; the controller runs
  as `darkpanel` and the agent as root, so each role gets its own key.

## Consequences

- Every release's migrations must keep the previous minor's binary working, and
  destructive schema changes wait a release.
- The `secret` tag drives encryption and redaction everywhere (see the shared
  contracts in the index).
- `darkpanel controller backup` and `restore` are M2 deliverables.
- Losing `controller.key` without a backup means re-enrolling every host.

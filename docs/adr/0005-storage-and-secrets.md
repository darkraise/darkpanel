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
  string, one writer connection. Each role has its own directory, because the
  write-ahead log creates files next to the database:
  `/var/lib/darkpanel/controller/controller.db` (directory and file owned by
  `darkpanel`) and `/var/lib/darkpanel/agent/agent.db` (root).
- Core tables use the prefix `core_`; each module uses `<module id>_`.
- Migrations: embedded SQL per module and role, numbered, forward-only, each in
  a transaction, recorded in `core_migrations` with a checksum. Each role runs
  its migrations when its service starts, before serving. A binary ignores
  `core_migrations` rows numbered above its own highest migration and verifies
  checksums only for the migrations it embeds. A changed checksum or failed
  core migration stops startup, which the update health check catches
  (ADR 0007); a failed module migration marks only that module unavailable
  (ADR 0002).
- **Expand, then contract.** A release's migrations must leave the schema usable
  by the previous minor release's binary. Destructive changes (drop, rename)
  ship only in a later release, after nothing reads the old shape. New columns
  are nullable or have defaults, so an older binary's inserts still succeed. A
  binary rollback therefore runs the old binary on the new schema without
  restoring data; updates step one minor release at a time (ADR 0007), so the
  old binary is never more than one minor behind the schema.
- Before migrating, each role copies its database with `VACUUM INTO` into its
  own directory, `backups/<from>-<to>-<timestamp>/` (mode 0600, last five
  kept), for disaster recovery only. No backup is restored automatically, and
  the agent's journal and revert records are never restored from one.
- **Secrets.** XChaCha20-Poly1305 with a random 24-byte nonce and associated
  data `<table>.<column>.<row id>`, where the row id is the table's declared
  primary key (a ULID), never the implicit SQLite rowid, which `VACUUM` may
  renumber; ciphertexts carry a key id so rotation can be added. Secret fields
  inside plan records and snapshots are encrypted the same way (ADR 0004). Each
  role has its own key, created atomically at install
  (`/etc/darkpanel/controller.key` owned by `darkpanel`,
  `/etc/darkpanel/agent.key` owned by root, both 0600). Agents hold only the
  secrets their own modules use (for example Nginx Proxy Manager credentials on
  its host).
- **Certificate authority key** is stored encrypted in `controller.db`.
  `darkpanel controller backup` exports the database and the controller key
  into one archive encrypted with a key derived from an operator passphrase
  with argon2id; `restore` is the lost-controller path in the runbook (R81).

## Alternatives considered

- `mattn/go-sqlite3`: rejected; needs CGO.
- PostgreSQL: rejected; an extra service on every controller host for a small
  data set.
- Down migrations: rejected; rarely tested, and expand-then-contract makes a
  binary rollback safe without them.
- Restoring the database on update rollback: rejected; see Context.
- One shared secret key for controller and agent: rejected; the controller runs
  as `darkpanel` and the agent as root, so each role gets its own key.
- A key readable only by root (the original R12 wording): rejected. The
  unprivileged controller must decrypt the credentials it uses, and a root
  decryption broker would add a privileged component that a compromised
  controller could still call. systemd `LoadCredential=` was also rejected; it
  ties the key path to systemd and needs a separate development fallback.
- Running migrations inside the update transaction: rejected; first install
  and development need them at service start anyway.

## Consequences

- Every release's migrations must keep the previous minor's binary working, and
  destructive schema changes wait a release.
- The `secret` tag drives encryption and redaction everywhere (see the shared
  contracts in the index).
- `darkpanel controller backup` and `restore` are built by the M8 Production
  rollout and runbook epic; the runbook (R81) depends on them.
- A migration must never make an older binary of the previous minor refuse to
  start.
- Losing `controller.key` without a backup means re-enrolling every host.

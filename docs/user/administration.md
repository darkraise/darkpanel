# Administer Darkpanel

This page lists what the controller itself does: users, API tokens, hosts,
modules on a host, the audit log, updates, and backup and restore. Every
action here has a CLI command, which is the action name without its `core`
prefix: `core.audit.list` is `darkpanel audit list`. The
[API reference](../api/README.md) describes the same actions for scripts.

## How actions work

- Every action has a minimum scope. A token or a session needs that scope or a
  higher one. `read` can only look, `operate` can change a host, and `admin`
  can change everything.
- An action that changes something shows a plan first, and the CLI asks
  `Apply? [y/N]`. `--yes` answers that question.
- A read action changes nothing.
- A compensable action can be undone by another action, which the entry names.
- An irreversible action cannot be undone. It also asks you to type a
  confirmation phrase, such as a host name, which `--confirmation <phrase>`
  gives on the command line.
- A few actions are served only on the controller socket, to root on the
  controller's host. They are marked below and work when the web interface and
  the API do not.

## Users

The first admin is created on the controller's host, as
[Create the first admin](first-admin.md) describes. These actions are all
served only on the controller socket and need the `admin` scope, except the
list, which needs `read`.

- `core.user.create`: `sudo darkpanel user create --username <name>` creates a
  pending admin user, asks for its password, and shows its TOTP secret once.
  Undo it with `core.user.delete`.
- `core.user.activate`: `sudo darkpanel user activate --username <name>`
  activates a pending user with a code from its authenticator. Undo it with
  `core.user.reset-totp`.
- `core.user.list`: `sudo darkpanel user list` lists the users.
- `core.user.reset-password`: `sudo darkpanel user reset-password --username <name>`
  sets a new password, ends the user's sessions and revokes its API tokens. It
  cannot be undone.
- `core.user.reset-totp`: `sudo darkpanel user reset-totp --username <name>`
  gives the user a new TOTP secret and returns it to pending, which ends its
  sessions. It cannot be undone.
- `core.user.delete`: `sudo darkpanel user delete --username <name>` deletes a
  user with its sessions and API tokens, and refuses to delete the last active
  admin. It cannot be undone.

[Recover from a lockout](lockout-recovery.md) uses these commands when nobody
can sign in.

## API tokens

A token belongs to the user who created it. Its scope is `read`, `operate` or
`admin`, and it expires after 90 days unless you set `expiresInDays`, from 1 to
365. The CLI reads a token from `DARKPANEL_TOKEN`.

- `core.token.create`: `darkpanel token create --name <name> --scope <scope>`
  needs `admin`. It creates a token and shows it once. Undo it with
  `core.token.revoke`.
- `core.token.list`: `darkpanel token list` needs `read`. It lists your tokens,
  newest first, revoked and expired ones included until they are purged.
- `core.token.revoke`: `darkpanel token revoke --id <id>` needs `admin`. It
  revokes one of your tokens, and it cannot be undone.

## Hosts

A host is pending until its agent enrolls, then enrolled, and revoked after
`core.host.revoke`. [Install Darkpanel](install.md) shows how to add one.

- `core.host.create`: `darkpanel host create --name <name>` needs `admin`. It
  creates a pending host and shows its one-hour enrollment token once. Add
  `--tailnet-join --tags <tag>` to get a bootstrap bundle that joins the host to
  the tailnet first. Undo it with `core.host.delete`.
- `core.host.list`: `darkpanel host list` needs `read`. It lists the hosts with
  the state of every module on each.
- `core.host.token`: `darkpanel host token --host-id <id>` needs `admin`. It
  issues a pending or revoked host, other than the local one, a new one-hour
  token that replaces any earlier one. It cannot be undone.
- `core.host.revoke`: `darkpanel host revoke --host-id <id>` needs `admin`. It
  revokes the host's agent certificates and its enrollment token and closes its
  live session. It cannot be undone.
- `core.host.delete`: `darkpanel host delete --host-id <id>` needs `admin`. It
  deletes a pending or revoked host, other than the local one. It cannot be
  undone.
- `core.host.enroll-local`: `sudo darkpanel host enroll-local` needs `admin` and
  is served only on the controller socket. It revokes the local host's agent
  credentials and issues a new enrollment token for `127.0.0.1:7443`. Use it
  when the controller's own agent lost its credentials. It cannot be undone.
- `core.host.update-policy`: `darkpanel host update-policy --host-id <id> --channel <channel>`
  needs `admin`. It sets the release channel the host follows, `stable` or
  `beta`, and its weekly two-hour update window in UTC, or none for updates on
  demand only. Undo it by setting the old policy.

## Modules on a host

A module is enabled per host. A disabled module's actions and resources answer
`module.unavailable` on that host.

- `core.module.list`: `darkpanel module list --host-id <id>` needs `read`. It
  lists the state of every compiled-in module on the host.
- `core.module.enable`: `darkpanel module enable --host-id <id> --module-id <module>`
  needs `admin`. It enables the module on the host and runs its preflight checks
  there again. Undo it with `core.module.disable`.
- `core.module.disable`: `darkpanel module disable --host-id <id> --module-id <module>`
  needs `admin`. It disables the module on the host. Undo it with
  `core.module.enable`.

## Audit log

- `core.audit.list`: `darkpanel audit list` needs `read`. It lists audit log
  entries, newest first, and you can filter them by `--actor`, `--host-id` or
  `--action`. Secret fields are redacted when an entry is recorded.

## Updates and rollouts

A host updates to a signed release on its channel. The
[versioning page](../versioning.md) explains the rules: an update moves at most
one minor version at a time, a new binary that fails its health check is rolled
back automatically, and the controller supports agents of its own minor version
and the one before. A rollout updates hosts one at a time, the controller's own
host first, and stops at the first failure.

- `core.update.apply`: `darkpanel update apply --host <host> --version <version> --channel <channel>`
  needs `operate`. It updates one host to a signed release. The change
  completes before the services restart, and `core.update.status` follows the
  update. Undo it with `core.update.rollback`.
- `core.update.rollback`: `darkpanel update rollback --host <host>` needs
  `operate`. It puts back the binary that the last update replaced. `--force`
  rolls back although an enrolled agent runs a newer version. It cannot be
  undone.
- `core.update.rollout`: `darkpanel update rollout --hosts <id> --version <version> --channel stable`
  needs `admin`. It updates the listed hosts in turn. Undo it with
  `core.update.rollout-cancel`.
- `core.update.rollout-cancel`: `darkpanel update rollout-cancel --rollout-id <id>`
  needs `admin`. It stops a rollout, so that no further host is updated.
  `--force` marks it cancelled at once, without waiting for a running update to
  finish. It cannot be undone.
- `core.update.rollouts`: `darkpanel update rollouts` needs `read`. It lists
  rollouts with their hosts, newest first.
- `core.update.status`: `darkpanel update status --host <host>` needs `read`. It
  lists the host's update and rollback transactions, newest first.

## Backup and restore

A backup holds the controller's database and its key, in one file that is
encrypted with a passphrase. These commands are written by hand and run on the
controller's host as root, over the controller socket.

### Back up the controller

```sh
sudo darkpanel controller backup --out <file>
```

The command asks for the passphrase twice, without showing it. To read it from
a file instead, pass `--passphrase-file <file>`; the file must be owned by root
and have mode 0600. `--out` names the backup file, which must not exist yet.
Keep the passphrase: the file cannot be opened without it. Only one backup runs
at a time.

The passphrase is stretched with argon2id, by default with 3 passes, 64 MiB of
memory and a parallelism of 4. To change them, use `--argon2-t` for the passes
(1 to 10), `--argon2-m` for the memory in KiB (65536 to 1048576) and
`--argon2-p` for the parallelism (1 to 16).

### Restore the controller

Restore into a host on which you ran `sudo darkpanel system install --role controller`.
Stop `darkpanel-controller.service` and `darkpanel-agent.service` first. Restore
refuses to run while either is active. Then run:

```sh
sudo darkpanel controller restore --in <file>
```

It asks for the passphrase, or reads `--passphrase-file`. It checks the whole
file and the restored database before it replaces anything. When it
finishes, it prints when the backup was taken and tells you to start both
services.

If a restore is cut short, for example by a power loss, start the controller
or run this to finish it or to roll it back:

```sh
sudo darkpanel controller restore --recover
```

`--recover` takes no other flag, and the controller service runs it before
every start, so the controller never starts on an unfinished restore.

### Settle the restore quarantine

A backup records work that was unfinished when it was taken. That work is
history, not an instruction. A restored controller therefore starts in
quarantine: it does not resume or replay unfinished changes, cloud operations
and rollouts, it closes its runtime listeners and marks its exposures as
needing repair, and it suspends the automatic update scheduler and the other
background sources. Logins and reads work. Changes to users and tokens, and applying any
action, are refused with `core.quarantined` until you release the quarantine.

Work through it on the controller's host, as root:

```sh
sudo darkpanel controller quarantine list
sudo darkpanel controller quarantine resolve <record> done
sudo darkpanel controller quarantine enroll-token <host>
sudo darkpanel controller quarantine release
```

- `darkpanel controller quarantine list` shows whether the quarantine is
  active and which records it holds.
- `darkpanel controller quarantine resolve <record> <done|never|repair>` settles
  a record by what happened to its work. `done` means the work finished and
  `never` means it was never applied, and both release the record's locks.
  `repair` keeps the locks and leaves the record to `darkpanel change repair`
  after the release. The controller checks with the agents first, and refuses
  `done` and `never` while the work may still be running or an agent the record
  names is not connected.
- `darkpanel controller quarantine enroll-token <host>` issues a one-hour,
  single-use token to enroll a host that is already in the restored database.
- `darkpanel controller quarantine release` ends the quarantine and starts the
  suspended sources. It is refused while records are unresolved. With
  `--keep-unresolved`, it releases anyway, leaves those records quarantined
  with their locks, and prints every lock that stays held and by which record.

Every `quarantine` command accepts `--json`, which prints the raw answer.

To bring agents back after a restore, see
[Re-enroll agents](../runbook.md#re-enroll-agents).

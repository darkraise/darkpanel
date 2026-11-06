# Runbook

This page lists the procedures to follow when something has already gone
wrong: a lockout, a failed update and a lost controller. Run each one as root on
the host itself. The commands use the agent socket (`--local`) or the
controller socket, never the network, so they work when the web interface, the
API or the network does not. Each procedure says what it changes and how to
check that it worked.

- [Recover from a lockout](user/lockout-recovery.md) covers a lost password or
  authenticator.
- [Administration](user/administration.md) explains the backup commands.
- [Install](user/install.md) explains how to install a host.

## Lockout

A lockout is a change that cut your own way in. Every module that can do that
has a guard and a revert, so the first step is almost always to wait for the
revert. For problems with signing in, see
[Recover from a lockout](user/lockout-recovery.md).

### Firewall break-glass

Use this when a firewall change cut you off from a host.

1. Wait. A pending firewall change reverts on the host after 60 seconds.
   `--confirm-timeout` on the command that applied the change sets another time,
   from 10 to 900 seconds. The revert needs neither the controller nor the
   network.
2. If you are still out, roll the change back as root on the host:

```sh
sudo darkpanel firewall rollback --local
```

   The command reverts the newest pending firewall change that no background
   task made. For a change that was already confirmed, it restores the
   snapshot taken before the change and confirms the restore. It shows what it
   is about to do and asks `Apply? [y/N]`. `--change <id>` picks a change,
   `--yes` skips the question, `--manual` lets the restore add or remove manual
   rules, and `--override firewall.lockout` plans the restore again with the
   lockout check overridden. Use that last option only when you are sure the
   restore is safe for your own connection.
3. To go back to a snapshot you choose, list the snapshots and restore one:

```sh
sudo darkpanel firewall snapshots get --local
sudo darkpanel firewall snapshot restore --local --id <id> --confirm
```

4. If UFW and the kernel disagree and nothing else works, repair the firewall
   state:

```sh
sudo darkpanel firewall state repair --local
```

The agent runs firewall changes one at a time, so a rollback waits for a change
that is still being applied. The module never shows a half-applied state to a
plan or a status read. To check that it worked, read the snapshots again and
try your connection.

### Tailscale preferences revert

Use this when a `tailscale.prefs.set` change, such as an exit node,
shields-up, `wantRunning`, SSH or routes, cut the node off. There is no
separate revert command: the change is revertible, and reverting it is the
procedure.

1. Wait for the agent to revert it. An unconfirmed preferences change reverts on
   the host after the confirm timeout, 60 seconds by default. The revert needs
   neither the controller nor the tailnet.
2. To see where the node stands, read its status on the host:

```sh
sudo darkpanel tailscale status get --local
```

3. If you can reach the controller, revert the change at once. The first
   command shows the change id, and the second reverts it:

```sh
darkpanel change list --state pending
darkpanel change revert <id>
```

4. If you can only reach the host, set the preferences back yourself. Write the
   fields to restore into a JSON file, for example:

```json
{"wantRunning": true, "shieldsUp": false}
```

   The fields are `wantRunning`, `advertiseRoutes`, `exitNode`,
   `advertiseExitNode`, `acceptRoutes`, `shieldsUp` and `runSSH`. Then apply the
   file:

```sh
sudo darkpanel tailscale prefs set --local --input <file> --yes --confirm
```

   If the lockout guard `tailscale.link` refuses, `--override tailscale.link`
   plans again with it overridden. Use it only when you know the change is safe.
5. If the revert itself failed, the change needs repair:

```sh
darkpanel change repair <id>
```

   Its option `outcome` is `restore`, the default, which puts back the values
   from before the change, or `keep`, which applies the values the change set.
   Pass it as `--input '{"outcome":"keep"}'`.

To check that it worked, read the status again.

### Panel listener revoke

Use this when the panel is exposed through Nginx Proxy Manager or a tunnel and
must go dark now, and you cannot use the web interface.

1. As root on the controller's host, revoke the listener:

```sh
sudo darkpanel panel listener revoke
```

   The command commits the revoked state and closes the listener and its open
   connections at once, through the controller socket. Revocation is permanent:
   no failure, timeout, restart or revert reopens it.
2. Finish the removal as an admin, from the web interface or the CLI:

```sh
darkpanel panel exposure remove
```

   Its plan has nothing left to revoke. It removes the proxy host or hostname,
   detaches the proxy, removes the network and deletes the firewall rule, which
   you confirm like the add's. If a step fails, the exposure stays revoked and
   the panel stays unreachable through it. Run `darkpanel change repair
   <change-id>` to repair the removal.
3. To expose the panel again, run a new `panel exposure add`.

## Failed update

A new binary that fails its health check rolls back by itself within two
minutes, as the [versioning page](versioning.md) explains. These procedures are
for a host that is still broken afterwards.

### Rollback

Put the previous binary back, as root on the host:

```sh
sudo darkpanel update rollback --local --yes
```

The command puts back the binary that the last update replaced. Once it runs,
nothing stops it, neither Ctrl+C nor a closed terminal. `--force` rolls back
although an enrolled agent runs a newer version than the previous binary.

To check that it worked, read the version and the agent's state:

```sh
darkpanel version
sudo systemctl status darkpanel-agent.service --no-pager
```

### Recover

Use this when a crash or a power loss cut a rollback or an update short, so that
the services stay stopped until the next boot:

```sh
sudo darkpanel update recover
```

If the rollback had started to replace the binary, the command finishes it and
restarts the services. If it had not, the command ends it. You can run it again
safely.

## Lost controller

The controller's database or key is gone. With a backup made by
`darkpanel controller backup` and its passphrase, you restore it, and the agents
come back. Without a backup, losing `controller.key` means installing a new
controller and enrolling every host again. See
[Install](user/install.md).

### Restore from backup

1. On the replacement host, install the controller role with the address the
   agents will dial. You can also use `install.sh --role controller`, as
   [Install](user/install.md) explains.

```sh
sudo darkpanel system install --role controller --agent-advertise <host>:<port>
```

2. Put the backup file and a passphrase file on the host. The passphrase file is
   owned by root with mode 0600. Without `--passphrase-file`, the restore asks
   for the passphrase. Remove the passphrase file once the restore is done.
3. Stop both services. A restore refuses while either runs. A stop you ask for
   ends the unit cleanly and does not start `darkpanel-rollback.service`.

```sh
sudo systemctl stop darkpanel-controller.service darkpanel-agent.service
```

4. Restore the backup:

```sh
sudo darkpanel controller restore --in <backup-file> --passphrase-file <passphrase-file>
```

   The command checks the whole file and the restored database before it
   replaces anything, keeps the old files under `.pre-<transaction>` names, and
   prints when the backup was taken. If it reports that the restore finished but
   could not remove an earlier restore's set-aside files, the restore is
   complete: remove those `.pre-` files by hand or let the next restore do it.
5. If a restore was cut short, for example by a power loss, finish or undo it:

```sh
sudo darkpanel controller restore --recover
```

   The command takes no other flag. The controller unit runs it before every
   start.
6. Start the services:

```sh
sudo systemctl start darkpanel-controller.service darkpanel-agent.service
```

7. The restored controller starts in quarantine. Unfinished changes, cloud
   operations and rollouts are history, and nothing replays them. Logins and
   reads work, and changes are refused with `core.quarantined`. Re-enroll the
   agents that are not connected first, as
   [Re-enroll agents](#re-enroll-agents) explains: a record that names an
   agent that is not connected cannot be resolved as `done` or `never`. Then
   list the records:

```sh
sudo darkpanel controller quarantine list
```

   Settle each record. `done` means the work finished, `never` means it never
   applied, and `repair` leaves it for `darkpanel change repair` after the
   release:

```sh
sudo darkpanel controller quarantine resolve <record> done
```

   Then end the quarantine:

```sh
sudo darkpanel controller quarantine release
```

   `--keep-unresolved` releases anyway and leaves those records, with their
   locks, quarantined. See
   [Settle the restore quarantine](user/administration.md#settle-the-restore-quarantine).

To check that it worked, run `sudo darkpanel controller quarantine list`: it
reports that the quarantine is no longer active.

### Unsettled restore journal

This section covers one refusal of `controller restore --recover`: a journaled
rename it cannot settle. A restore journals every rename before it runs.
Recovery settles a journaled rename by which of its two names exists. When both
exist or neither exists, it stops, keeps the journal, and the controller does
not start.

Recovery also stops, keeps the journal and keeps the controller from starting
when the installed database fails verification (`the installed database failed
verification`), when a staged file is not a single regular file (`is not a
single regular file`), and when it does not trust the journal itself (`is not a
single regular file owned by uid` or `does not belong to this controller`).
This section does not cover those refusals.

1. Recognise it. The controller unit fails at start, and the unit's systemd log
   shows that a journaled rename cannot be settled:

```sh
sudo journalctl -u darkpanel-controller.service
```

   Look for `backup: a journaled rename cannot be settled`, followed by the two
   names and whether each exists. The journal is
   `/var/lib/darkpanel/controller/restore-<transaction>.journal`.
2. Stop both services. The controller unit restarts after a failure, and each
   start runs the recovery again, so a restart could act on the files while you
   change them:

```sh
sudo systemctl stop darkpanel-controller.service darkpanel-agent.service
```

   Before you change anything, copy everything the restore touched to a
   directory that only root can read: the directory
   `/var/lib/darkpanel/controller/` and every `/etc/darkpanel/controller.key*`
   file.

```sh
sudo install -d -m 0700 /root/restore-copy
sudo cp -a /var/lib/darkpanel/controller /root/restore-copy/controller
sudo cp -a /etc/darkpanel/controller.key* /root/restore-copy/
```

   A later successful restore removes the `.pre-` files of earlier restores, so
   this copy is the only record of them. Nothing below deletes what the copy
   holds. It includes the controller keys, so once the controller runs again
   and you no longer need it, move it off the host or remove it.
3. End the stuck transaction by hand. Remove the journal
   `restore-<transaction>.journal` and `restore-<transaction>.journal.new`, and
   the staged files of that transaction, the names ending in
   `.restore-<transaction>` beside `controller.db` and beside `controller.key`
   (the database also has staged `-wal` and `-shm` files). Leave the
   `.pre-<transaction>` files, which hold the installation as it was before the
   restore.
4. A restore refuses a host without `/etc/darkpanel/controller.key`. If that
   name is missing, rename `controller.key.pre-<transaction>` back to it first;
   the restore replaces the key with the one in the backup.
5. Restore again from the same backup file. The backup file is the whole target
   state, so this works whichever names were present:

```sh
sudo darkpanel controller restore --in <backup-file> --passphrase-file <passphrase-file>
```

   Then continue with steps 6 and 7 of
   [Restore from backup](#restore-from-backup).
6. If you have no backup file, put the installation back as it was from the
   `.pre-<transaction>` files instead. Rename each one to its name without the
   suffix, moving aside any file that already has that name, and then start the
   services.

### Re-enroll agents

Agents that can still reach the controller with valid credentials come back by
themselves. Re-enroll each agent that stays offline in `darkpanel host list`.

1. On the controller's host, as root, while the quarantine is active, issue an
   enrollment token:

```sh
sudo darkpanel controller quarantine enroll-token <host>
```

   The command prints a one-hour, single-use token once. It works for a host
   that is already in the restored database, only during the quarantine, and only
   when the controller runs with an `--agent-advertise` address. If the host is
   enrolled, it sets it to pending.
2. On the agent's host, as root, give the token to the agent. The token file is
   owned by root with mode 0600. Without `--token-file`, the command prompts, or
   reads standard input.

```sh
sudo darkpanel agent enroll --token-file <token-file>
```

3. `agent enroll` prints that a running agent needs no restart, because it uses
   the new credentials from its next connection attempt. Restart it anyway, so
   that it drops the session it holds and dials with the new credentials at
   once:

```sh
sudo systemctl restart darkpanel-agent.service
```

4. Check that the host is online:

```sh
darkpanel host list
```

5. The controller's own agent cannot be enrolled with a quarantine token. After
   you release the quarantine, run this on the controller's host:

```sh
sudo darkpanel host enroll-local
```

   The command needs `admin`, is served only on the controller socket, revokes
   the local host's agent credentials and prints a new token for
   `127.0.0.1:7443`. Repeat steps 2 and 3 there. See
   [Administration](user/administration.md).

If you already released the quarantine, `quarantine enroll-token` is refused
with "the controller is not in restore quarantine; use darkpanel host token".
Issue the token with `darkpanel host token --host-id <id>` instead. It needs
`admin` and works for a pending or revoked host other than the local one. A
host that `darkpanel host list` shows as enrolled is refused with "is enrolled;
revoke it before issuing a new token": run
`darkpanel host revoke --host-id <id>` first, which also needs `admin`. Then
continue with steps 2 and 3.

Do not run the installer again for the agent role to re-enroll an agent. With
saved credentials that the controller still accepts, it keeps them and ignores
the new token.

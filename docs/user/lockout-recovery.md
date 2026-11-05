# Recover from a lockout

This page gets you back in when you cannot sign in, when a firewall change cut
you off from a host, or when an update left a host broken.

## Before you start

Every command on this page runs as root on the host itself. They use the
controller socket or the agent socket, never the network, so they work when the
web interface and the API do not.

- Commands about users go to the controller socket, so run them on the
  controller's host.
- `--local` sends an agent-side command to the local agent's socket instead of
  to the controller. Use it on a host whose controller you cannot reach, for
  example `sudo darkpanel hostinfo summary get --local`.

## You cannot sign in

List the users and their states:

```sh
sudo darkpanel user list
```

Then choose the command that matches your problem:

- A lost password: `sudo darkpanel user reset-password --username <name>` asks
  for a new password of 12 to 128 characters and sets it. It ends the user's
  sessions and revokes the user's API tokens, so create new tokens afterwards.
- A lost authenticator: `sudo darkpanel user reset-totp --username <name>`
  prints a new TOTP secret as a QR code, asks for a code from it and activates
  the user again. It ends the user's sessions. Until you finish, the user
  cannot sign in.
- A user you no longer want: `sudo darkpanel user delete --username <name>`
  deletes the user with its sessions and API tokens. It refuses to delete the
  last active admin.

None of the three can be undone. Each shows its plan, asks `Apply? [y/N]` and
then asks you to type the confirmation phrase, which is the username. `--yes`
answers the first question, and `--confirmation <name>` gives the phrase.

After too many failed attempts the controller answers that you should try
again later. Wait, and the block lifts by itself.

## A firewall change cut you off

A firewall change stays pending until someone confirms it. If nobody does, the
host undoes the change by itself after 60 seconds. `--confirm-timeout` on the
command that applied the change sets another time, from 10 to 900 seconds.
Most of the time, waiting is the whole recovery.

If you are already out and the timer is not running, sign in on the host's
console or over another route, and run:

```sh
sudo darkpanel firewall rollback --local
```

The command finds the newest firewall change that no background task made. It
reverts the change if it is still pending. If the change was already
confirmed, it restores the snapshot taken before the change and confirms the
restore in the same command. It shows what it is about to do and asks
`Apply? [y/N]`. These options change it:

- `--change <id>` names the change to roll back.
- `--yes` skips the question.
- `--manual` lets the snapshot restore add or remove manual rules, which its
  plan lists.
- `--override firewall.lockout` plans the restore again with the lockout check
  overridden. Use it only when you know that the restore is safe for your own
  connection.

To go back to a snapshot of your choice, list them and restore one:

```sh
sudo darkpanel firewall snapshots get --local
sudo darkpanel firewall snapshot restore --local --id <id> --confirm
```

The `tailscale` and `panel` modules can also change how you reach a host. Their
module pages explain what each guards against.

## An update left a host broken

A new binary that fails its health check is rolled back by itself within two
minutes, as the [versioning page](../versioning.md) explains. If the host is
still broken, put the previous binary back yourself:

```sh
sudo darkpanel update rollback --local --yes
```

This puts the previous binary back. Once it runs, nothing stops it: neither
Ctrl+C nor a closed terminal does.

If a crash or a power loss cut the rollback short, the services can stay
stopped until the next boot. Run this instead of waiting for a reboot:

```sh
sudo darkpanel update recover
```

If the rollback had started to replace the binary, `update recover` finishes it
and restarts the services. If it had not, `update recover` ends it. You can run
it again safely.

## Restore the controller from a backup

If the controller's database or key is lost or damaged, restore a backup with
`darkpanel controller restore`. A restore needs both services stopped and
leaves unfinished work in quarantine until you settle it. The
[administration page](administration.md) explains the whole procedure.

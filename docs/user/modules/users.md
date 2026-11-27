# Users and SSH

## Overview

The `users` module manages a host's login accounts, their authorized SSH keys,
and the SSH daemon's hardening settings. It lists root and every account with
a UID 1000 to 59999, creates and deletes accounts, locks and unlocks them,
sets and removes passwords, adds them to and removes them from the `sudo`
group, adds and removes keys, and writes sshd's settings to a file of its own.

The module is on for every host. Reads need the `read` scope; every change
needs `admin`. Creating an account, adding a key, setting a password and
adding to `sudo` each grant a way in, so they also need a session elevated by
your authenticator code within the last 15 minutes: the web UI asks for the
code, and the API answers `auth.step_up_required` until you give it. API
tokens cannot step up, so those four actions work only from a signed-in
browser session.

## Before you start

- Every account change runs the host's own tools: `useradd`, `usermod`,
  `userdel`, `chpasswd`, `gpasswd` and `passwd -S`. Darkpanel's own system
  users are never listed or changed.
- The SSH daemon settings need `/etc/ssh/sshd_config` to include
  `sshd_config.d/*.conf` before any other directive, as the stock file of
  every supported host does. Otherwise the SSH daemon half is unavailable and
  says why.
- Key actions need sshd's default key files. When `sshd -T` shows an
  `authorizedkeysfile` other than `.ssh/authorized_keys
  .ssh/authorized_keys2`, they are unavailable with that reason.
- Keep a second way in while you harden SSH: an open session, or the
  [console](console.md).

## Use it in the web UI

The **Users and SSH** tab lists the login accounts with their name, sudo
membership, number of keys and whether they are locked. **Add a user** opens
a form for the name, whether it **Can use sudo**, an optional password, typed
twice, and an optional public key; **Review change** shows the plan. Each
row's actions menu has **Keys**, **Set password**, **Remove password**, **Add
to sudo** or **Remove from sudo**, **Lock** or **Unlock**, and **Delete**.
**Keys**, or the account's name, opens its **Authorized keys** with each key's
type, fingerprint and comment, a **Remove** action per key and **Add a public
key**.

The **SSH daemon** card shows the values sshd runs with now: **Password
login**, **Root login**, **Login attempts**, **Login grace time**, **Allowed
users** and **Allowed groups**. **Edit SSH hardening** opens a form that starts
from those values; **Review change** plans it. The change applies, then waits
for your confirmation with a countdown and reverts by itself at the deadline:
open a new SSH session before you confirm, so you know you can still sign in.

## Use it from the CLI

Commands reach the controller with an API token, which [Create the first
admin](../first-admin.md#next-an-api-token-for-the-cli) explains how to
create. Each command below is the generated command of one action, and the
[users API reference](../../api/users.md) lists its input and output.
`--host <id or name>` picks the host.

| Action | Command | What it does | Asks you |
|---|---|---|---|
| `users.user.list` | `darkpanel users user list` | Lists root and the accounts with UID 1000 to 59999: name, UID, home, shell, groups, sudo, locked, password set and number of keys. | Nothing; it only reads. |
| `users.user.create` | `darkpanel users user create` | Creates an account with its home and its own group, optionally in `sudo`, with a password and keys. | A step-up code. `change undo` cannot reverse it; delete the account with `users.user.delete`. |
| `users.user.lock` | `darkpanel users user lock` | Locks an account: its expiry ends key and password sign-in. | Nothing. `change undo` cannot reverse it; unlock the account with `users.user.unlock`. |
| `users.user.unlock` | `darkpanel users user unlock` | Unlocks an account, restoring the expiry it had. | Nothing. `change undo` cannot reverse it; lock the account with `users.user.lock`. |
| `users.user.delete` | `darkpanel users user delete` | Deletes an account, with `--remove-home` its home and mail spool too. | The account name typed. It cannot be undone. |
| `users.password.set` | `darkpanel users password set` | Sets an account's password. | A step-up code, and the password. |
| `users.password.remove` | `darkpanel users password remove` | Removes an account's password: its hash becomes `*`. | Nothing. |
| `users.sudo.add` | `darkpanel users sudo add` | Adds an account to the `sudo` group. | A step-up code. `change undo` cannot reverse it; remove it with `users.sudo.remove`. |
| `users.sudo.remove` | `darkpanel users sudo remove` | Removes an account from the `sudo` group. | Nothing. `change undo` cannot reverse it; add the account back with `users.sudo.add`, which asks for a step-up code. |
| `users.key.list` | `darkpanel users key list` | Lists an account's keys: type, bits, SHA256 fingerprint, comment and options. | Nothing; it only reads. |
| `users.key.add` | `darkpanel users key add` | Authorizes one public key for an account. | A step-up code. `change undo` cannot reverse it; remove the key with `users.key.remove`. |
| `users.key.remove` | `darkpanel users key remove` | Removes a key by its fingerprint. | Nothing. `change undo` cannot reverse it; add the key back with `users.key.add`, which asks for a step-up code. |
| `users.sshd.get` | `darkpanel users sshd get` | Reads sshd's effective settings from `sshd -T` and the ones Darkpanel set. | Nothing; it only reads. |
| `users.sshd.apply` | `darkpanel users sshd apply` | Writes sshd's settings and reloads sshd. | Your confirmation before the deadline, or it reverts. |
| `users.sshd.repair` | `darkpanel users sshd repair` | Rewrites Darkpanel's sshd file from the last confirmed settings, or removes it when none were confirmed, and reloads sshd. | Nothing. |

```sh
darkpanel users user list --host web-1
darkpanel users key add --host web-1 --name deploy --key "ssh-ed25519 AAAA... deploy@laptop"
darkpanel users user lock --host web-1 --name deploy
darkpanel users user delete --host web-1 --name deploy --remove-home --confirmation deploy
darkpanel users sshd apply --host web-1 --input sshd.json --confirm-timeout 120
darkpanel change confirm <change-id>
```

The password of `users password set` and of `users user create` has no flag:
the command asks for it without echo on a terminal, or reads one line from
standard input.

### Accounts

A new account name matches `^[a-z_][a-z0-9_-]{0,31}$` and must not exist yet.
Its shell is one of `/etc/shells`, `/bin/bash` when you give none. A password
is 8 to 256 characters with no control character; it goes to `chpasswd` on
standard input and is hashed with the host's configured method, yescrypt on
every supported host. It is never logged and never returned, and its sealed
copy is erased from the plan on the agent and the controller once the change
ends or the plan expires.

Locking records the account's current expiry and runs
`usermod --expiredate 1`, which blocks key and password sign-in alike;
unlocking restores the recorded expiry. A lock never touches the password
hash, so a password locked by hand stays locked. Root cannot be locked.
`users.user.list` counts an account as locked when its expiry date is today or
earlier; a `!` password alone is the normal key-only account.

Deleting refuses root and an account with a running process. Its plan warns,
naming them, when Darkpanel [jobs](jobs.md) run as that account.

### Keys

A new key is one public key line without options. Accepted types are
ed25519, ecdsa, sk-ed25519, sk-ecdsa, and RSA of at least 3072 bits. Keys
already in the file keep their options and their bytes as they are.

The key file is written only when the home directory and `.ssh` are owned by
the account or root and not writable by group or others, sshd's own
StrictModes rule. `.ssh` is created as 0700 and `authorized_keys` as 0600,
owned by the account. A symlinked home, `.ssh` or key file is refused.

### SSH daemon settings

`users.sshd.apply` writes `/etc/ssh/sshd_config.d/00-darkpanel.conf`. sshd
takes the first value it reads, and this file sorts before the others, such as
`50-cloud-init.conf`, so its values win. The file holds what you set of
`passwordAuthentication`, `kbdInteractiveAuthentication`, `permitRootLogin`
(`yes`, `prohibit-password` or `no`), `maxAuthTries` (1 to 10),
`loginGraceTime` (10 to 600 seconds), `allowUsers`, `allowGroups`,
`clientAliveInterval` (0 to 3600 seconds), `clientAliveCountMax` (0 to 10) and
`x11Forwarding`; an omitted field is left to sshd's other files. The flags
cover `--permit-root-login`, `--allow-users` and `--allow-groups`; give the
rest in a JSON document with `--input`:

```json
{"passwordAuthentication": false, "kbdInteractiveAuthentication": false, "permitRootLogin": "prohibit-password"}
```

The apply runs `sshd -t` and reverts at once when it fails, then reloads
`ssh.service` when it is active; under socket activation new connections read
the file. It then compares `sshd -T` with what you set. The change waits for
confirmation, 60 seconds unless `--confirm-timeout` says otherwise, and
reverts by itself at its deadline, also across an agent restart.
`users.sshd.repair` puts the file back to the last confirmed settings.

`AllowUsers`, `AllowGroups`, `DenyUsers` and `DenyGroups` add up across files
instead of taking the first value. Darkpanel manages `allowUsers` and
`allowGroups` only when no other file under `/etc/ssh` sets any of the four;
otherwise those fields are read-only and `users.sshd.get` names the file.

### The lockout guard

`users.sshd.apply`, `users.key.remove`, `users.user.lock`,
`users.user.delete` and `users.password.remove` carry the guard
`users.no_way_in`: it refuses a change after which no account could sign in.
An account counts when sshd's effective allow and deny lists admit it, it is
not locked, and either it has a key while `sshd -T` shows
`pubkeyauthentication yes` and an `authenticationmethods` that a key alone
satisfies (root only when `PermitRootLogin` allows keys), or it has a password
while password or keyboard-interactive sign-in is on. The guard's text names
the accounts it considered. It cannot see the overrides of a `Match` block,
and says so when `sshd_config` or a drop-in has one. You can override it with
`--override users.no_way_in` when you have another way in.

## What it changes on the host

- Accounts, groups, passwords, `sudo` membership and `~user/.ssh/authorized_keys`
  files, through the host's own tools.
- `/etc/ssh/sshd_config.d/00-darkpanel.conf`, an owned file.
- `/var/lib/darkpanel/users`, where a lock records each account's previous
  expiry as `expiry-<name>`.

`sudo darkpanel uninstall` removes `00-darkpanel.conf` and the expiry records.
sshd then falls back to its other files and its defaults, which on many hosts
turns password sign-in back on; its plan says so. Login users, their
authorized keys and their passwords stay: they are the host's data. See
[Uninstall Darkpanel](../uninstall.md).

## When something goes wrong

| Code | What it means here | What you do |
|---|---|---|
| `plan.guard_failed` | `users.no_way_in` refused: after the change no account could sign in. The message names the accounts considered. | Add a key or a password for an account first, or override the guard when you have another way in. |
| `auth.step_up_required` | The action grants a way in and your session is not elevated. | Enter your authenticator code when the web UI asks. |
| `auth.step_up_unavailable` | Your user has no confirmed authenticator, or the call came from an API token. | Use a signed-in browser session of a user with an authenticator. |
| `plan.confirmation_required` | The delete needs the account name typed. | Give `--confirmation` with the name. |
| `module.unavailable` | The SSH daemon or key half is unavailable: `sshd_config` does not include `sshd_config.d/*.conf` first, or sshd uses other key files. The message says which. | Fix `sshd_config`, or manage that part by hand. |
| `request.invalid_input` | An input is invalid, for example a name that exists, a shell not in `/etc/shells`, a key type or size that is refused, or `sshd -t` rejected the file. | Read the message and correct the input. |

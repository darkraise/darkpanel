# Intrusion prevention with fail2ban

## Overview

The `fail2ban` module installs and configures fail2ban, which bans addresses
that keep failing to sign in over SSH. It reads the sign-in failures from the
journal and bans through nftables, in fail2ban's own table `inet f2b-table`.
The module shows the jails and the banned addresses, lets an operator unban an
address, and lets an admin ban one, change the settings, and install or remove
the package.

The module is on for every host. Reads need the `read` scope, unbanning needs
`operate`, and everything else needs `admin`.

## Before you start

- The host installs packages from its configured APT sources. Install adds
  `fail2ban` and `python3-systemd`.
- When CrowdSec runs on the host (`crowdsec.service` is loaded), the page says
  **CrowdSec runs on this host**. Darkpanel then installs nothing, but still
  manages a fail2ban that is already installed, and a settings change warns
  that CrowdSec is also running.
- fail2ban never bans the controller's addresses, so the agent keeps its link
  to the controller, nor loopback and the tailnet's ranges, as "Settings"
  describes.

## Use it in the web UI

The **Intrusion prevention** tab of a host shows **fail2ban is not installed**
with an **Install fail2ban** button until it is installed. Then it shows:

- **Jails**: each running jail with its banned and failed counts.
- The banned addresses, with each ban's **Jail**, **Since** and **Until**, or
  **Until unbanned**. An operator has an **Unban** button on each row.
- **Settings**, for an admin: **Ban for (minutes)**, the window failures are
  counted in, **Failures before a ban**, and **Ban repeat offenders for a
  week**, which turns on the `recidive` jail. **Add an address or network**
  adds to the addresses that are never banned; the fixed ones are listed as
  **Always kept: loopback, the controller and the tailnet** and cannot be
  removed. **Review change** shows the plan.

The [firewall](firewall.md) page of the host lists fail2ban as a known
neighbour, `fail2ban: <n> banned addresses`, and keeps managing UFW beside
it.

## Use it from the CLI

Commands reach the controller with an API token, which [Create the first
admin](../first-admin.md#next-an-api-token-for-the-cli) explains how to
create. Each command below is the generated command of one action, and the
[fail2ban API reference](../../api/fail2ban.md) lists its input and output.
`--host <id or name>` picks the host.

| Action | Command | What it does | Asks you |
|---|---|---|---|
| `fail2ban.status.get` | `darkpanel fail2ban status get` | Reads fail2ban's version, service state, jails, settings, and whether CrowdSec is installed. | Nothing; it only reads. |
| `fail2ban.ban.list` | `darkpanel fail2ban ban list` | Lists the banned addresses per jail, with each ban's start and end. | Nothing; it only reads. |
| `fail2ban.package.install` | `darkpanel fail2ban package install` | Writes Darkpanel's settings, then installs `fail2ban` and `python3-systemd` and starts fail2ban. | Nothing. Undo with `change undo`, which removes the packages. |
| `fail2ban.package.remove` | `darkpanel fail2ban package remove` | Removes `fail2ban` and `python3-systemd`; Darkpanel's settings file stays. | Nothing. Undo with `change undo`. |
| `fail2ban.settings.apply` | `darkpanel fail2ban settings apply` | Writes the jail settings and reloads fail2ban. | Nothing. Undo with `change undo`, which puts the previous settings back. |
| `fail2ban.ban.add` | `darkpanel fail2ban ban add` | Bans an address in a jail. | Nothing. Undo with `change undo`. |
| `fail2ban.ban.remove` | `darkpanel fail2ban ban remove` | Unbans an address in a jail. | Nothing. Undo with `change undo`. |

```sh
darkpanel fail2ban package install --host web-1
darkpanel fail2ban ban list --host web-1 --jail sshd
darkpanel fail2ban ban remove --host web-1 --jail sshd --address 198.51.100.7
darkpanel fail2ban settings apply --host web-1 --input fail2ban.json --recidive
```

### Settings

`fail2ban.settings.apply` writes `/etc/fail2ban/jail.d/zz-darkpanel.local`,
which fail2ban reads last, so its values win. Install writes it with the
defaults first, then installs the package, so the package's own ban action
never runs. The file holds:

- `[DEFAULT]` with `banaction = nftables-multiport`,
  `banaction_allports = nftables-allports` and `backend = systemd`, the ban
  time (1 minute to 30 days, 10 minutes by default), the window failures are
  counted in (1 minute to 1 day, 10 minutes by default) and the failures that
  ban an address (1 to 20, 5 by default).
- `ignoreip`: `127.0.0.1/8 ::1`, the addresses the agent resolves its
  controller endpoint to, `100.64.0.0/10 fd7a:115c:a1e0::/48`, then your own
  entries, at most 64. The fixed entries cannot be removed: the whole list
  goes in `ignoreIp`, so start from what `fail2ban status get` reports.
- `[sshd]`, enabled on sshd's effective ports.
- `[recidive]`, when you turn it on: an address banned again and again is
  banned for a week on every port. On the controller's own host it bans only
  the SSH ports, so it can never drop the panel's listener.

The apply runs `fail2ban-client -t`, reloads fail2ban and reads its status
back. When recidive is on and your own client address is outside the ignore
list, the plan carries the guard `fail2ban.self_ban`, which offers to add it;
override it with `--override fail2ban.self_ban` to keep the list as it is.

`fail2ban.ban.add` refuses an address of the ignore list and your own client
address.

### Roll back with care

The firewall module accepts `inet f2b-table` only in the exact shape fail2ban's
`nftables-multiport` and `nftables-allports` actions create, and lists it as a
known neighbour. An agent rolled back to a release before this module does not
know that table: it reports the firewall unsupported while any address is
banned. Unban the addresses, or remove fail2ban, before you roll back.

## What it changes on the host

- The packages `fail2ban` and `python3-systemd`, and the `fail2ban.service`
  unit, which install starts when the package left it stopped.
- `/etc/fail2ban/jail.d/zz-darkpanel.local`, an owned file; install creates
  `/etc/fail2ban/jail.d`, root-owned and mode 0755, when it is absent.
- While addresses are banned, fail2ban's own nftables table `inet f2b-table`.

`sudo darkpanel uninstall` removes `zz-darkpanel.local`; fail2ban then falls
back to its package defaults, and its plan says so. It leaves the packages
installed.

## When something goes wrong

| Code | What it means here | What you do |
|---|---|---|
| `plan.guard_failed` | `fail2ban.self_ban` refused: recidive could ban your own address on every port. | Add your address to the ignore list, or override the guard. |
| `system.removal_refused` | Removing fail2ban would also remove other packages; the message names them. | Remove it by hand on the host. |
| `module.unavailable` | "fail2ban is not installed on this host" for a change that needs it, or "CrowdSec is installed on this host" for an install. | Install fail2ban first, or keep CrowdSec. |
| `request.invalid_input` | An input is invalid: a removed fixed ignore entry, an unknown jail, a malformed address, an address already banned or not banned, or a setting out of range. | Read the message and correct the input. |

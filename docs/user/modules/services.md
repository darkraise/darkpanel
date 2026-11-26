# Services and journal

## Overview

The `services` module shows a host's systemd units and its journal, and
starts, stops, restarts, reloads, enables and disables services, sockets and
timers. It talks to systemd over D-Bus and reads the journal with
`journalctl`. Mounts, paths and targets are listed but never changed, and
powering the host off or rebooting it stays with `system.host.reboot` on the
[System](system.md) tab.

The module is on for every host. Reading units and the journal needs the
`read` scope; starting, stopping, restarting and reloading need `operate`;
enabling and disabling need `admin`. Some units need more, as "Units that are
refused or guarded" describes.

## Before you start

- Nothing to install: every supported host runs systemd and its journal.
- A member sees this tab only for the hosts their grants name, and on the
  controller's own host only as a viewer.

## Use it in the web UI

The **Services** tab of a host lists its units with their **Unit**,
**Description**, **State** and **At boot** columns. **Search units** filters by
name or description, and **State** shows only the units in one state, such as
failed. An operator sees an actions menu on each row with **Start**,
**Stop**, **Restart** and **Reload**; an admin also sees **Enable** and
**Disable**. Every change opens the change dialog with its plan. A unit whose
change needs its name typed asks for it there.

**View logs** opens the journal panel for that unit. The panel filters by
**Unit**, **Priority** (at most the priority you pick, or **Every priority**)
and **Since** (**Last 15 minutes**, **Last hour** or **Last day**), and
**Follow** keeps it updated as new entries arrive. When the stream drops, it
shows **Reconnecting…** and resumes.

Darkpanel's own units show their menu disabled with "Darkpanel manages its own
units".

## Use it from the CLI

Commands reach the controller with an API token, which [Create the first
admin](../first-admin.md#next-an-api-token-for-the-cli) explains how to
create. Each command below is the generated command of one action, and the
[services API reference](../../api/services.md) lists its input and output.
`--host <id or name>` picks the host.

| Action | Command | What it does | Asks you |
|---|---|---|---|
| `services.unit.list` | `darkpanel services unit list` | Lists the services, sockets, timers, mounts, paths and targets with their state; `--type` and `--state` filter. | Nothing; it only reads. |
| `services.unit.get` | `darkpanel services unit get` | Shows one unit: state, memory, tasks, restarts, start time, unit file text up to 64 KiB and drop-in paths. | Nothing; it only reads. |
| `services.journal.query` | `darkpanel services journal query` | Reads a page of journal entries by unit, priority, time range and message text. | Nothing; it only reads. |
| `services.journal-follow` | `darkpanel services journal-follow` | Follows the journal with the same filters, starting with the last `--lines` entries (100 by default, at most 500). | Nothing; it only reads. |
| `services.unit.start` | `darkpanel services unit start` | Starts a service, socket or timer. | Nothing. Undo with `change undo`, which stops it. |
| `services.unit.stop` | `darkpanel services unit stop` | Stops a service, socket or timer. | Nothing. Undo with `change undo`, which starts it. |
| `services.unit.restart` | `darkpanel services unit restart` | Restarts a service, socket or timer. | Nothing; a restart cannot be undone. |
| `services.unit.reload` | `darkpanel services unit reload` | Reloads a unit's configuration. | Nothing; a reload cannot be undone. |
| `services.unit.enable` | `darkpanel services unit enable` | Enables a unit at boot. | Nothing. Undo with `change undo`. |
| `services.unit.disable` | `darkpanel services unit disable` | Disables a unit at boot. | Nothing. Undo with `change undo`. |

```sh
darkpanel services unit list --host web-1 --type service --state failed
darkpanel services unit restart --host web-1 --unit nginx.service
darkpanel services unit restart --host web-1 --unit ssh.service --confirmation ssh.service
darkpanel services journal query --host web-1 --unit nginx.service --priority 3 --substring timeout
darkpanel services journal-follow --host web-1 --unit nginx.service --lines 50
```

`--since` and `--until` take epoch seconds. A message filter matches case
insensitively and literally, never as a pattern. A journal query runs at most
10 seconds; a page ends at 1000 entries or 2 MiB of entries, with `more` set
and a `cursor` that `--cursor` takes for the next page. A message longer than
8 KiB is cut and marked `truncated`.

A host runs at most 8 journal followers at once; one more is refused with
`core.stream_limit`.

### Units that are refused or guarded

Start and restart are refused with `services.protected` and
"Use Reboot on the System tab" for units that power the host off, reboot it, suspend it or leave
the running system: `systemd-poweroff`, `systemd-reboot`, `systemd-halt`,
`systemd-kexec`, `systemd-soft-reboot`, `systemd-suspend`,
`systemd-hibernate`, `systemd-hybrid-sleep`, `systemd-suspend-then-hibernate`,
`systemd-exit`, `systemd-storagetm`, `system-update-cleanup`, `rescue`,
`emergency` and `debug-shell`. The same holds for any unit whose `Requires=` or
`Wants=` names `sleep.target`, `shutdown.target`, `umount.target` or
`final.target`, any unit with `RefuseManualStart=yes` or `AllowIsolate=yes`,
and a timer or socket that triggers such a unit. A unit whose `SuccessAction`
is not `none` is refused start, restart and stop. Names are compared on every
name of the unit, so an alias does not get around the rule.

Every `darkpanel*` unit is refused with "Darkpanel manages its own units":
jobs change through the [Jobs](jobs.md) tab, the agent and controller through
updates. `dbus.service` and `dbus.socket` are refused stop, restart and
disable, because the agent talks to systemd through them.

These need more than the action's own scope:

- Units another Darkpanel module manages (`ssh.service`, `ssh.socket`,
  `sshd.service`, `tailscaled.service`, `docker.service`, `ufw.service`,
  `fail2ban.service`) and the firewall loaders (`nftables.service`,
  `netfilter-persistent.service`, `firewalld.service`): every action needs
  admin and the unit name typed as `--confirmation`. The guard
  `services.module_change` refuses while the managing module has a change
  applying or awaiting confirmation.
- A unit whose `FailureAction`, `JobTimeoutAction` or `StartLimitAction` is not
  `none`: admin and the unit name typed.
- The network units `networking`, `systemd-networkd`, `NetworkManager` and
  `systemd-resolved`: stop and disable are refused by the guard
  `services.network_stack`, because the agent reaches the controller through
  the network stack; restart needs the unit name typed.
- `tailscaled.service`: while the agent's own link to the controller runs over
  the Tailscale interface, stop and restart are refused by the guard
  `services.tailnet_link`, and disable plans with a warning that it takes
  effect at the next boot.
- `systemd-journald.service`: stop, restart and disable need the unit name
  typed.

None of these guards can be overridden.

## What it changes on the host

Nothing of its own: it writes no file and creates no unit. A change starts,
stops, reloads, enables or disables the unit you named, through systemd, and
`darkpanel uninstall` has nothing of this module to remove.

## When something goes wrong

| Code | What it means here | What you do |
|---|---|---|
| `services.protected` | The unit is one of the refused units above, or it is not a service, socket or timer. The message says why. | Use the System tab to reboot or power off, or change the unit by hand on the host if you must. |
| `plan.guard_failed` | A guard refused: `services.network_stack`, `services.tailnet_link` or `services.module_change`. | Wait for the other module's change to finish, or reach the host another way first. |
| `plan.confirmation_required` | The unit needs its name typed, and `--confirmation` was missing or named something else. | Type the unit name exactly. |
| `request.invalid_input` | The unit name or a filter is invalid. | Correct the input. |
| `not_found` | No unit of that name is loaded on the host. | Check the name with `services unit list`. |
| `core.stream_limit` | Eight journal followers already run on the host. | Close a journal panel or a `journal-follow` command. |

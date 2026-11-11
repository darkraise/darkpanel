# Uninstall Darkpanel

`darkpanel uninstall` removes Darkpanel from the host you run it on. Run it on
every host you want to clear, the controller's host included. Removing a host
from the fleet is a separate step on the controller; see
[Administer Darkpanel](administration.md).

## What uninstall does

```sh
sudo darkpanel uninstall
```

The command must run as root. It first prints its plan, one numbered step at a
time, with every file, directory, unit and user that it will remove, as the
host's ownership manifest lists them. Then it asks `Proceed? [y/N]`. Answer
`y` to go on.

The steps run in this order:

1. `barrier` makes the agent refuse new changes. It refuses to start while an
   update runs; see [Unfinished changes](#unfinished-changes).
2. `services` stops and disables the Darkpanel services and the units of
   modules.
3. `agent lock` makes sure that no agent is running.
4. `modules` runs the uninstall step of each module that has one.
5. `marked blocks` removes the blocks that Darkpanel added to files that other
   software owns.
6. `files` removes the files and directories that Darkpanel created.
7. `units` removes the systemd unit files and reloads systemd.
8. `binary` removes `/usr/local/lib/darkpanel` and the link
   `/usr/local/bin/darkpanel`.
9. `data` removes `/etc/darkpanel` and `/var/lib/darkpanel`, with the
   `darkpanel` user when `darkpanel system install` created it, or keeps them
   with `--keep-data`. A `darkpanel` user that existed before the install is
   kept.

It prints one line for each finished step. If a step fails, the command stops
and names the steps that did not run. Run `sudo darkpanel uninstall` again
after you fix the cause: a new plan picks up where the last one stopped.

If you answer anything but `y` or `yes`, the command removes nothing and exits
with status 3. Without a terminal, the command cannot ask, so it refuses with
status 3 unless you give `--yes`.

## Flags

- `--keep-data` keeps the databases, backups, update records, keys, agent
  credentials and users, so a later install finds them.
- `--yes` uninstalls without asking `Proceed? [y/N]`.
- `--module-option <module>.<key>=<value>` sets an option of a module's
  uninstall step. You can repeat it. The next section lists the options.
- `--stop-tasks` stops the task units of unfinished changes, instead of
  refusing to uninstall while one is active.
- `--abort` clears the uninstall barrier of an uninstall that stopped before it
  stopped the services, so the agent accepts changes again. It takes none of
  `--stop-tasks`, `--keep-data` and `--module-option`.

## Module options

A module can offer a choice for what its uninstall step leaves on the host.
Give a choice as `--module-option <module>.<key>=<value>`. The plan that the
command prints lists every option with its default, and a module you name no
option for uses its default.

The `firewall` module has the option `rules`:

- `firewall.rules=remove` is the default. It deletes the rules that Darkpanel
  added and keeps the manual ones.
- `firewall.rules=keep` leaves every rule as it is.
- `firewall.rules=baseline` restores the firewall as it was before Darkpanel's
  first change.

With any of the three, the uninstall also finishes the Docker cleanups that an
earlier change left, and removes the block and the chains that the Docker
integration put in place. If a foreign rule sits in the `DOCKER-USER` chain of
a host where the integration left something, the cleanup refuses it and the
uninstall fails once the rules work is done. Remove that rule and run the
uninstall again. On a host without UFW, the uninstall leaves the rules as they
are.

The `tailscale` module has the option `package`:

- `tailscale.package=keep` is the default. It leaves the `tailscale` package
  installed.
- `tailscale.package=remove` removes the `tailscale` package, but only if
  Darkpanel installed it.

For example, to restore the original firewall and remove a Tailscale package
that Darkpanel installed:

```sh
sudo darkpanel uninstall --module-option firewall.rules=baseline --module-option tailscale.package=remove
```

The command refuses a module, a key or a value that no module declares before
it removes anything.

## Keep your data

With `--keep-data`, the uninstall removes the services, units and binary, but
keeps `/var/lib/darkpanel`, `/etc/darkpanel` and the system users that
Darkpanel created. That includes the databases, the backups, the update
records, the keys and the agent credentials. It also clears the uninstall
barrier, so a later install finds the data and accepts changes.

Without `--keep-data`, the data and the keys are removed. Take a backup of the
controller first if you may need its data again; see
[Administer Darkpanel](administration.md).

## Unfinished changes

A change that is still running holds a task unit. Without `--stop-tasks`, the
uninstall refuses to go on while a change is unfinished or a task unit is
active, and names the units. Wait for them to finish, or run:

```sh
sudo darkpanel uninstall --stop-tasks
```

This sets the barrier first, so no new task starts, and then stops every active
task unit.

An update that is still running holds the update reservation. With or without
`--stop-tasks`, the uninstall refuses to start while it does, names the update
operation, and changes nothing. Wait for the update to finish, or roll it back
with `sudo darkpanel update rollback --local`. If the update already finished
but its reservation was left behind, `sudo darkpanel update recover` clears it.

## Abort an uninstall

If an uninstall stopped before it stopped the services, for example because
you interrupted it, the barrier is still set and the agent refuses new
changes. Clear it:

```sh
sudo darkpanel uninstall --abort
```

The agent accepts changes again. The abort is audited as this host's local root
user. Once the uninstall has stopped the services, there is nothing to abort,
and the command says so. Run the uninstall again to finish it.

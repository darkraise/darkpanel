# System

The `system` module manages a host's operating system packages with apt: it
shows pending updates and installed packages, upgrades, installs and removes
packages, repairs an interrupted package run, switches automatic security
updates on or off, tracks whether the host needs a reboot, and installs,
updates and uninstalls Docker Engine. It runs in the agent on each host. Its
page in the web interface is `/hosts/<id>/system`, with the tabs Overview,
Updates and Packages.

## Requirements

The host must run one of the supported releases: Ubuntu 22.04, 24.04 or 26.04,
or Debian 12 or 13, on amd64 or arm64. On any other release the module reports
itself unavailable. Automatic updates need the `unattended-upgrades` package;
the reboot state lists outdated services only when `needrestart` is installed.

Commands for a host take `--host <host>`; you can leave it out when you have
one host. The agent-side commands also take `--local`, which sends them to the
agent's socket on the host you are on, as root.

## Actions and resources

A compensable action can be undone with `darkpanel change undo <id>`, and the
table says what the undo does. An irreversible action cannot be undone: its
plan asks you to type the host's name, which `--confirmation <phrase>` gives on
the command line. `--yes` skips the `Apply? [y/N]` question.

| Name | Command | Kind | Minimum scope | What it does |
|---|---|---|---|---|
| `system.updates` | `darkpanel system updates get` | Read | `read` | Lists the upgradable packages with their versions and whether each is a security update, the packages a full upgrade would remove, the held-back packages, when the index was last refreshed, the sources that failed, and whether dpkg was interrupted. |
| `system.packages` | `darkpanel system packages get` | Read | `read` | Lists the installed packages with their version, architecture and description, and why a package is protected. |
| `system.reboot` | `darkpanel system reboot get` | Read | `read` | Shows whether a reboot is required, the packages that require it, the running and newest kernel, and the services that run outdated code. |
| `system.autoupdates` | `darkpanel system autoupdates get` | Read | `read` | Shows whether `unattended-upgrades` is installed and whether automatic updates are on. |
| `system.docker` | `darkpanel system docker get` | Read | `read` | Shows whether Docker Engine is absent, installed from Docker's repository, or installed from the distribution, with its version, whether an update is available, and its container, image and volume counts. |
| `system.packages.search` | `darkpanel system packages search` | Read | `read` | Finds up to 50 package names that contain a term, with their candidate version and whether each is installed. |
| `system.index.refresh` | `darkpanel system index refresh` | Compensable, nothing to undo | `operate` | Refreshes the package index of every configured source. |
| `system.packages.upgrade` | `darkpanel system packages upgrade` | Irreversible | `operate` | Upgrades every package, or only the named installed ones. You type the host's name. |
| `system.packages.install` | `darkpanel system packages install` | Compensable | `operate` | Installs the named packages. Undo removes exactly the packages this change installed. |
| `system.packages.remove` | `darkpanel system packages remove` | Irreversible | `operate` | Removes the named packages, with their configuration files when `purge` is set. You type the host's name. |
| `system.packages.repair` | `darkpanel system packages repair` | Irreversible | `operate` | Finishes an interrupted package run and fixes broken dependencies. You type the host's name. |
| `system.autoupdates.set` | `darkpanel system autoupdates set` | Compensable | `operate` | Turns automatic updates on or off. Undo restores the setting it replaced. |
| `system.host.reboot` | `darkpanel system host reboot` | Irreversible | `operate` | Reboots the host 15 seconds after the change applies. You type the host's name. |
| `system.docker.engine-install` | `darkpanel system docker engine-install` | Compensable | `operate` | Installs Docker Engine from Docker's repository. Undo removes the packages and repository files it added. |
| `system.docker.engine-update` | `darkpanel system docker engine-update` | Irreversible | `operate` | Upgrades the Docker Engine packages to the versions Docker's repository offers. You type the host's name. |
| `system.docker.install` | `darkpanel system docker install` | Composite | `admin` | Installs Docker Engine on the host named by `hostId` and, with `firewallIntegration`, turns on the firewall's Docker integration; see [Docker](docker.md). |
| `system.docker.uninstall` | `darkpanel system docker uninstall` | Irreversible | `operate` | Removes Docker Engine and the repository files Darkpanel wrote; with `purge`, also deletes every container, image and volume. You type the host's name, or `purge docker on <host>` for a purge. |

Every action that runs apt holds the lock `system.apt`, so two package changes
on one host never run at once. An upgrade may run for up to 60 minutes, an
install, a removal, a repair or a Docker action for 30, and a refresh for 10.
Package names must be package names and nothing else: a name that is not an
exact package apt knows (install) or an exact installed package (upgrade,
remove) is refused before apt runs.

## Overview tab

The Overview tab shows four tiles:

- **Updates** counts the pending and security updates, says when the index was
  last refreshed and lists the sources that failed. Refresh reloads the index;
  Upgrade all upgrades every package. When dpkg was interrupted, the tile says
  so and offers Repair instead.
- **Reboot** says whether the host needs a reboot and why: the packages that
  asked for one, a newer kernel than the running one, and the services that
  still run outdated libraries. Reboot asks for the host's name and reboots it
  15 seconds later; the host shows offline until its agent reconnects.
- **Automatic updates** switches `unattended-upgrades` on or off. Darkpanel
  writes its setting to `/etc/apt/apt.conf.d/52darkpanel-auto-upgrades`, which
  sorts after the distribution's `20auto-upgrades` and so takes precedence.
- **Docker Engine** shows the installed version and offers Install, Update and
  Uninstall; see [Docker](docker.md).

While the host is offline, the tiles keep their last values greyed out and
every action is disabled.

## Updates tab

The Updates tab lists the upgradable packages in two groups, Security and
Other updates, each with its current and new version. Select packages and
upgrade them, or upgrade all. Two more groups explain what an upgrade leaves:
Will be removed lists the packages a full upgrade removes, and Held back lists
the packages apt keeps back, either because they are held (`apt-mark hold`) or
because Ubuntu is still phasing the update in.

## Packages tab

The Packages tab lists the installed packages. Search finds packages to
install by any part of their name. A protected package shows why it is
protected and cannot be removed here.

## Protected packages

A package change whose simulation would remove a protected package is refused
with `system.protected_package` before anything changes, whether you named the
package or apt would remove it as a consequence. Protected are:

- the package manager (`apt`, `dpkg`), the init system (`systemd`,
  `systemd-sysv`) and `dbus`;
- remote access (`openssh-server`, `sudo`, and `sudo-rs` on Ubuntu 26.04);
- the network stack (`netplan.io`, `systemd-resolved`, `ifupdown`), `ufw` and
  `tailscale`;
- the bootloader (`shim-signed` and every `grub-` package) and the running
  kernel's `linux-image-` and `linux-modules-` packages;
- `iptables` and `nftables` while the firewall module is enabled;
- every package dpkg marks essential or of required priority;
- the Docker Engine packages, which only the Docker uninstall and the undo of
  a Docker install remove.

## Errors

| Code | Meaning | What to do |
|---|---|---|
| `system.apt_busy` | Another apt or dpkg run holds the lock. | Wait for it to finish, then try again. |
| `system.dpkg_interrupted` | An earlier package run was interrupted. | Run Repair (`system.packages.repair`). |
| `system.broken_packages` | apt reports unmet dependencies. | Run Repair, then plan the change again. |
| `system.disk_full` | The host ran out of disk space. | Free space, then try again. |
| `system.repo_signature` | A source did not fetch or verify. | Check the source and its signing key; the error quotes apt's output. |
| `system.plan_stale` | The package set changed between the plan and the apply. | Plan the change again and review the new plan. |
| `system.sim_unparsed` | apt printed a simulation Darkpanel cannot read. | Nothing changed; run the same command by hand to see why. |
| `system.name_invalid` | A name is not a package name, not known to apt, or not installed. | Check the name. |
| `system.protected_package` | The change would remove a protected package. | Remove it by hand only if you mean to. |
| `system.removal_refused` | The change would remove packages you did not name. | Remove them by hand first if that is intended. |
| `system.unattended_absent` | `unattended-upgrades` is not installed. | Install it, then switch automatic updates on. |
| `system.change_pending` | A change on the host is still applying or awaiting confirmation. | Finish or settle it before rebooting. |
| `system.logind_refused` | logind refused the reboot. | The error names logind's reason, such as an inhibitor. |
| `system.apt_failed` | apt failed for another reason. | The error quotes the failing package and the end of apt's output. |

The Docker errors (`system.docker_present`, `system.docker_repo_conflict`,
`system.docker_keyring`, `system.docker_labelled`, `system.firewall_pending`,
`system.firewall_integration_on`, `system.firewall_flag_mismatch` and
`system.purge_refused`) are described on the [Docker](docker.md) page.

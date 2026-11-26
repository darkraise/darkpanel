# ADR 0020: Host administration access

**Requirements:** none

## Status

Accepted

## Context

ADR 0009 keeps every module inside two fences: programs run only from an
allowlist of absolute paths with fixed argument vectors, and files are written
only at paths and marked blocks the module's manifest declares. Those fences
are what make an action's plan an honest description of what it will do, and
what lets `darkpanel uninstall` reverse everything Darkpanel wrote.

The host administration modules (`services`, `users`, `fail2ban`, `console`,
`jobs` and `tuning`) exist to manage what the operator owns on a host, not
what Darkpanel owns: any systemd unit, any login account, any file. A root
terminal and a file manager cannot be expressed as declared argument vectors
and declared paths at all. This record states each place where these modules
step outside ADR 0009, the bound that replaces the fence there, and what stays
forbidden.

The controller judges who may act. On a host where the console or jobs module
is on, a compromised controller can therefore act as root there, whatever the
agent's own checks say. An owner who does not accept that for a host needs a
switch the controller cannot turn.

## Decision

- **Console shell.** `platform.Shell` is the one code path that runs a program
  outside the exec allowlist: root's login shell from `/etc/passwd` (else
  `/bin/bash`, else `/bin/sh`) with `-l` on a new PTY, a fixed environment and
  the agent's fixed `PATH`, moved over D-Bus into its own transient scope
  `darkpanel-console-<id>.scope` so the agent unit's kill mode never ends what
  the admin started. Bounds: the console module is off by default per host;
  every console request needs admin on the host; each terminal open needs a
  fresh TOTP code, exchanged for a single-use ticket valid 30 seconds and
  bound to the session and host; the agent refuses unless its own copy of the
  module flag is on and the host's policy file does not deny the module; at
  most 4 terminals per host and 8 per user; a terminal closes after 30 minutes
  without input and when its sign-in session ends; the agent journals each
  open and close, never the content.
- **Console files.** `platform.ConsoleFiles` reads and writes any path as root
  without a manifest declaration. Bounds: the same admin, enablement and
  policy checks as the shell; content reads and every write need a session
  elevated by a TOTP code within the last 15 minutes; writes, renames and
  deletes are refused under `/proc`, `/sys`, `/dev`, `/run`, `/boot/efi`,
  `/etc/darkpanel`, `/var/lib/darkpanel` and the Darkpanel binary, and on any
  ancestor of those; reads and downloads are refused under `/proc`, `/sys`,
  `/dev`, `/etc/darkpanel` and `/var/lib/darkpanel`; the check runs on the
  resolved target, with the parent opened through
  `openat2(RESOLVE_NO_SYMLINKS)`; a write goes through a temporary file the
  agent records before it writes, and every mutation journals an intent before
  the first byte and a result before it answers.
- **Undeclared units.** The `services` module starts, stops, restarts,
  reloads, enables and disables units it does not declare. Bounds: only
  `.service`, `.socket` and `.timer` units; units that power the host off,
  reboot or suspend it, or pull in the final targets, are refused start; every
  `darkpanel*` unit is refused; the D-Bus units and the network stack are
  guarded; units another Darkpanel module manages need admin, the unit name
  typed, and no change of that module in flight.
- **Home files.** `platform.HomeFiles` writes inside user-owned directories,
  only `~user/.ssh/authorized_keys`. Bounds: the home comes from
  `/etc/passwd` and is opened with `openat2(RESOLVE_NO_SYMLINKS)`; it and
  `.ssh` must be owned by the user or root and not writable by group or
  others, sshd's own StrictModes rule; the file is opened with `O_NOFOLLOW`,
  must be a regular file of at most 1 MiB, and is written by temporary file and
  rename relative to the directory descriptor.
- **Unit sets.** The ownership kind `unit-set` declares a directory and a
  file-name pattern instead of fixed names. The jobs module writes
  `darkpanel-job-<slug>.service` and `.timer` in `/etc/systemd/system`,
  matching `^darkpanel-job-[a-z0-9][a-z0-9-]{0,39}\.(service|timer)$`; each
  file is recorded in the ownership store when written and removed from it when
  deleted, and `ScopeUnits` admits names that match the pattern. Uninstall
  stops and disables the recorded timers before it removes the units.
- **Swap file.** The tuning module creates one swap file with `fallocate`, at
  the one fixed path `/darkpanel.swap`, with a marked block in `/etc/fstab`.
  Resize, remove and uninstall refuse while swapped memory exceeds available
  memory less 256 MiB.
- **What stays forbidden.** No module other than `console` gets a shell, and
  the console's providers are wrapped by the agent runtime so that no other
  module can reach them. Jobs run through systemd units Darkpanel writes, never
  through the shell provider. Every other exec addition of these modules
  (`journalctl`, `sshd -t` and `-T`, `useradd`, `usermod`, `userdel`,
  `chpasswd`, `gpasswd`, `passwd -S`, `fail2ban-client`, `systemd-analyze
  calendar`, `fallocate`, `mkswap`, `swapon`, `swapoff`) is an allowlist rule
  with a fixed argument shape, as ADR 0009 requires.
- **Host data.** Login users, their authorized keys and their passwords are
  the host's data, not Darkpanel's: uninstall leaves them in place. The files
  Darkpanel writes for sshd, fail2ban and the kernel parameters are its own and
  uninstall removes them; the uninstall plan names what each removal does to
  the host.
- **Duplex channel.** A module may declare channels, served on the agent at
  `GET /agent/v1/channels/{module}/{name}` with `Upgrade:
  darkpanel-channel/1` and bridged by the controller to a WebSocket. It is an
  additive extension of protocol version 1 (ADR 0003): hello is unchanged, and
  the controller offers a channel only to an agent that reports the module.
- **The controller is trusted with root where these modules are on.** Step-up
  and enablement are both judged by the controller, and the agent's copy of the
  module flag is pushed by the controller, so neither defends against a
  compromised controller. On a host with `console` or `jobs` enabled, a
  compromised controller holds root.
- **Host-local deny switch.** `/etc/darkpanel/agent-policy.json` lists
  `console`, `jobs` or both in `denyModules`. Only root on the host changes it,
  with `darkpanel agent policy deny|allow <module>...` through the agent
  socket. The agent reads it at start and on that command, reports a denied
  module unavailable with "Turned off on this host by its policy file", refuses
  to enable it whatever the controller pushes, and its shell, file provider
  and job timers check it beside the flag. It is the bound an owner sets on a
  host where a compromised controller must not gain root.

## Alternatives considered

- **A terminal through SSH from the controller.** It needs a key on every
  host, an SSH listener the panel can reach and a second identity system, and
  it moves the root credential to the controller. Rejected.
- **A file manager over declared paths only.** It cannot edit the files an
  operator needs to edit, which are by nature not known in advance. Rejected;
  the refused prefixes protect what Darkpanel itself depends on instead.
- **Jobs as cron lines or through the console shell.** Cron entries have no
  ownership record, no unit state and no journal of their own, and a job
  through the shell would make the jobs module a second shell. Rejected in
  favour of units of a recorded unit set.
- **Trusting the agent's flag as a defence.** The controller pushes that flag,
  so it is no defence against the controller. Rejected; the host-local policy
  file is the switch only root on the host can turn.

## Consequences

- `internal/platform` stays the only package that runs programs or writes host
  files; `platform.Shell`, `platform.ConsoleFiles` and `platform.HomeFiles` are
  its only providers outside the declared-path rule.
- The threat model states that a compromised controller holds root on a host
  with `console` or `jobs` enabled, and names the policy file as the bound.
- A host whose owner denies the console and jobs keeps every other module.
- The console guide tells operators that an agent restart hangs up an open
  terminal as a dropped SSH connection does.
- The ownership store gains `unit-set` entries, which uninstall reverses with
  the other owned paths.

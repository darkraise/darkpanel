# ADR 0016: System module

**Requirements:** none

## Status

Proposed

## Context

The owner asked on 2026-11-19 to update and upgrade a server, install and
remove packages, and install Docker from the web UI. Each of these runs apt
as root on a managed host, which no module did except the Tailscale install
with its own fixed vectors. Three approaches were weighed: a new module, the
same actions inside the Docker module, and generic package actions only.

The module's natural id, `system`, was a reserved top-level CLI name: ADR
0011 says a module can never take a reserved name, and ADR 0012 reserved
`system` for the hand-written `darkpanel system` group, whose
`darkpanel system install` has shipped.

## Decision

- **A new module.** `system` is an agent-side module, enabled by default,
  whose preflight checks only the operating system and architecture. It owns
  package updates, package install, remove and repair, the reboot-required
  state and a scheduled reboot, automatic updates, and the Docker Engine
  install, update and uninstall. Every action that runs apt holds the one
  lock key `system.apt`.
- **The CLI name.** `system` leaves `core.ReservedNames`. The hand-written
  `darkpanel system` group adopts the module through
  `cli.Options.AdoptModules`, so `darkpanel system install` stays and the
  module's generated commands sit beside it. A module may take a CLI-reserved
  name only when the hand-written group of that name adopts it, and no
  generated command may replace a hand-written subcommand. This narrows ADR
  0011's "a module can never take a reserved name" and ADR 0012's
  reservation of `system`.
- **Exec rules.** A separate rule set for `/usr/bin/apt-get` keeps Tailscale's
  vectors and environment unchanged (the runner accepts several rules for
  one path). Every system apt and dpkg run gets `DEBIAN_FRONTEND=noninteractive`
  and `NEEDRESTART_SUSPEND=1`, so no prompt blocks and needrestart restarts
  no service. Allowed: `apt-get update` of every source or of Docker's list
  alone; the plan simulation, the task's re-simulation and the non-locking
  read simulation of the offered verbs (`install`, `install --only-upgrade`,
  `dist-upgrade`, `remove`, `purge`, `-f install`), the plan's lock probe,
  and their mutating runs, each matched against the exact vector the module
  builds; `apt-cache policy` and `pkgnames`; `dpkg-query` with one fixed
  format; `dpkg --audit`; `dpkg --force-confdef --force-confold
  --configure -a`; `apt-config dump`; `needrestart -b -r l`. Each is safe
  because a name reaches apt only after `--`, only after a pattern that
  refuses a leading `-` and any `:`, `=` or `/`, and only when it is an exact
  entry of `apt-cache pkgnames` (install) or an exact installed package
  (remove, purge, upgrade), so apt never reads a name as an option, a version,
  an action suffix or a regular expression. Mutating runs keep their output's
  tail rather than being killed for its length, since a killed dpkg leaves
  the package database interrupted. The `apt-cache` read is capped at 32 MiB
  instead of the runner's 4 MiB default, because `pkgnames` already prints
  3.46 MiB on a stock Ubuntu 22.04 image; it still fails rather than
  truncates over the cap.
- **Protected packages.** A package change whose simulation removes an
  essential or required package, the remote-access, network, boot and
  firewall packages, the running kernel or the Docker packages is refused
  before anything changes. Only `system.docker.uninstall` and the undo of
  `system.docker.engine-install` may remove the five Docker packages; every
  other protected removal still refuses there.
- **Reboot through logind.** `system.host.reboot` calls
  `org.freedesktop.login1.Manager.ScheduleShutdown("reboot", <15 s ahead>)`
  on the system bus. No exec rule for `reboot` or `shutdown` is added, an
  inhibitor lock or a shutdown already scheduled refuses with logind's own
  reason, and the reboot is refused while another change on the host is
  applying or awaiting confirmation.
- **`core.Step.DeferPlan`.** A composite step may defer its plan to its
  cursor during apply, as a step with `${steps...}` references already does:
  the composite's plan neither dry-runs nor hashes it. The Docker install
  needs it, because the firewall's Docker integration cannot plan before
  Docker exists.
- **`core.ActionSpec.Rechecks`.** An action names the modules whose
  availability its change can alter. When such a change ends, the controller
  runs those modules' preflight on the host and updates `core_module_state`,
  so the Docker module becomes available after the Docker install without a
  reconnect.
- **Bounded purge.** `platform.Purge` deletes `/var/lib/docker` and
  `/var/lib/containerd` and nothing else. It works through directory
  descriptors, unlinks a symlink without following it, refuses a path that is
  a symlink or a mount point or holds one, and refuses to descend onto
  another device. The uninstall's plan runs the same checks, so the method's
  refusal after the packages are gone only guards a change between plan and
  apply. A `data-root` set in `/etc/docker/daemon.json` is named and left
  alone.
- **Docker's repository.** The install writes Docker's keyring, embedded in
  the binary and pinned by fingerprint, and one source line, and refuses
  while another entry names `download.docker.com`. It refreshes Docker's list
  alone and refuses any fetch or signature failure. The uninstall removes
  only the files the ownership store records as Darkpanel's.
- **Trust.** Installing a package runs its maintainer scripts as root. That
  is the operator's explicit choice: confirmed by applying a plan that lists
  every package, typed (the host name, or `purge docker on <host>`) for the
  irreversible actions, and audited. It is equivalent in power to the
  existing Docker and Compose actions.
- **Scopes.** The class defaults: `operate` for the agent-side package,
  reboot and Docker uninstall actions, `admin` for the `system.docker.install`
  composite.

## Alternatives considered

- The same actions inside the Docker module: it reworks the availability
  model of a closed module, which is unavailable exactly when Docker is
  absent.
- Generic package actions only: they cannot add Docker's repository, which
  the Docker install needs.
- Renaming the module: the spec names it `system`, and the UI, API and CLI
  would disagree.
- Moving `darkpanel system install`: it is a shipped command that install
  scripts and documentation call.
- An exec rule for `systemctl reboot` or `shutdown -r`: it cannot schedule
  with logind's refusals, and adds a program that could stop the host at once.

## Consequences

- A host package change is a declared, planned, audited action, and package
  names never reach apt unchecked.
- The CLI's reserved names are no longer absolute; each exception needs a
  hand-written group that adopts the module.
- Composite authors can defer a step's plan, at the cost of a plan that does
  not cover that step.
- A change can now alter another module's availability, and the controller
  rechecks it when the change ends.
- Docker data is deleted only on an explicit, typed purge, and only the two
  named directories.

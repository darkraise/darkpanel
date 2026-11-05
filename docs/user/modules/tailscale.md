# Tailscale

The `tailscale` module manages the Tailscale node on one host. It installs the
Tailscale package from Tailscale's own repository, logs the node in and out of
a tailnet, changes the node's preferences, and shows its status, its peers and
its latency to Tailscale's relay servers. It protects you from cutting your own
access with a guard, and it can pin the package to one version. It runs in the
agent on each host. Its page in the web interface is `/hosts/<id>/tailscale`,
with the status, peers, latency, preferences and the install or update panel.
The [tailnet](tailnet.md) module manages the account and the policy of the
tailnet itself.

## Requirements

- A supported operating system and architecture: Ubuntu 22.04 (jammy) or 24.04
  (noble), or Debian 12 (bookworm) or 13 (trixie), on amd64 or arm64. The
  module checks nothing else, so it is available on a host before Tailscale is
  installed.
- For everything except `tailscale.package.install`, `tailscaled` must be
  running and answer on its socket. While it does not, the actions fail with
  `tailscale.unavailable`. Install the package first, which "Package" explains.

Commands for a host take `--host <host>`; you can leave it out when you have
one host. The agent-side commands also take `--local`, which sends them to the
agent's socket on the host you are on, as root.

## Actions and resources

A revertible action stays pending until you confirm it, and the agent reverts
it by itself when you do not. A compensable action can be undone afterwards
with `darkpanel change undo <id>`, and the table says what the undo does. An
irreversible action cannot be undone, and its plan asks you to type the name of
the host; `--confirmation <name>` gives it on the command line. For every
changing action, `--yes` skips the `Apply? [y/N]` question. For a revertible
one, `--confirm` confirms the change at once, and `--confirm-timeout` sets the
seconds it waits, from 10 to 900. The default is 60 seconds.

| Name | Command | Kind | Minimum scope | What it does |
|---|---|---|---|---|
| `tailscale.status` | `darkpanel tailscale status get` | Read | `read` | Shows the node, its tailnet, its preferences and its peers. |
| `tailscale.latency` | `darkpanel tailscale latency get` | Read | `read` | Measures the latency to each DERP region. |
| `tailscale.prefs.set` | `darkpanel tailscale prefs set` | Revertible | `operate` | Changes the node's preferences. |
| `tailscale.prefs.repair` | `darkpanel tailscale prefs repair` | Revertible | `operate` | Repairs a preferences change whose revert failed. |
| `tailscale.node.login` | `darkpanel tailscale node login` | Undone by an inverse | `operate` | Logs the node in to a tailnet with an auth key. Undo logs it out, only if it was logged out before. |
| `tailscale.node.logout` | `darkpanel tailscale node logout` | Irreversible | `operate` | Logs the node out of its tailnet. You type the host name. |
| `tailscale.package.install` | `darkpanel tailscale package install` | Undone by an inverse | `operate` | Installs Tailscale from its apt repository, and adds the repository when it is absent. Undo removes the package, only if this change installed it. |
| `tailscale.package.update` | `darkpanel tailscale package update` | Irreversible | `operate` | Updates the package to the repository's current version. You type the host name. |
| `tailscale.package.pin` | `darkpanel tailscale package pin` | Undone by an inverse | `operate` | Installs exactly one version and holds it. Undo rolls back to the earlier version and hold state. |
| `tailscale.package.unpin` | `darkpanel tailscale package unpin` | Undone by an inverse | `operate` | Releases the hold that a pin set, without changing the installed version. Undo holds the package again. |

## Status and latency

`tailscale.status` shows:

- the backend state: `NoState`, `NeedsLogin`, `NeedsMachineAuth`, `Stopped`,
  `Starting` or `Running`;
- the node itself: its Tailscale addresses, its MagicDNS name and its tags,
  and the tailnet it belongs to. There is no node while it is logged out;
- the preferred DERP region, the relay region the node uses as its home;
- whether Tailscale SSH is on;
- the preferences, every field set;
- each peer, with whether it is online and how this node reaches it:
  `direct`, with the UDP endpoint in use; `derp`, with the region; `peer-relay`;
  or `none` when nothing was sent to it yet. A peer is also marked when it is
  this node's exit node or offers itself as one;
- the health warnings that `tailscaled` reports.

`tailscale.latency` measures two things for each region of the DERP map, and
labels each:

- STUN latency: the median of three STUN requests from the host to the
  region's STUN endpoints. It shows as unavailable when the region has no STUN
  endpoint or the host blocks UDP.
- DERP HTTPS probe latency: the median of three HTTPS requests to the region's
  DERP servers. It measures how fast the server answers from this host. It does
  not measure a relayed path between two nodes. It shows as unavailable when the
  request fails.

DERP is Tailscale's relay service, which carries traffic between two nodes
that cannot reach each other directly. Each measurement that could not be made
shows its reason beside "unavailable".

## Preferences

`tailscale.prefs.set` changes the preferences you give and leaves the others
alone. The fields are:

- `wantRunning`: whether the node is connected.
- `advertiseRoutes`: the subnet routes the node advertises, as prefixes. It
  never includes `0.0.0.0/0` or `::/0`; use `advertiseExitNode` for that.
- `exitNode`: the exit node in use, by its stable id or Tailscale address. An
  empty value clears it.
- `advertiseExitNode`: whether the node offers itself as an exit node.
- `acceptRoutes`: whether the node accepts the subnet routes other nodes
  advertise.
- `shieldsUp`: whether the node refuses every incoming connection.
- `runSSH`: whether the node runs Tailscale SSH.

The change is revertible. It records the previous values of exactly the
fields you set, and a revert puts exactly those back.

When a revert of a preferences change fails, the change needs repair. Run
`darkpanel change repair <id>`, which plans `tailscale.prefs.repair`. Its
option `outcome` is `restore`, the default, which puts back the values from
before the change, or `keep`, which applies the values the change set. Pass it
with `--input '{"outcome":"keep"}'`. The repair is checked by reading the
preferences back, and only then does it report `restored` or `kept`.

## The link guard

Changing the node can cut the connection you are using, including the agent's
own link to the controller when it runs over the tailnet. Before it applies, the
plan checks the flows that matter: SSH sessions, your request, the agent's link
and the connections between the controller and its agents. It decides for each
flow whether it depends on Tailscale. Tailscale SSH sessions are not visible
to the host, so while `runSSH` is on the guard assumes one may be your access
path.

The guard `tailscale.link` fails the plan when:

- you turn `wantRunning` off, log out, or log in again with `relogin`, and a
  flow depends on Tailscale or Tailscale SSH is on;
- you turn `shieldsUp` on, and a flow arrives over Tailscale or Tailscale SSH
  is on;
- you turn `runSSH` off while it is on, and your request did not arrive by a
  path that avoids Tailscale;
- you turn `acceptRoutes` off, and a flow uses a route accepted from the
  tailnet;
- you change or clear the `exitNode`, and a flow leaves through it;
- you remove an advertised route, or stop advertising an exit node, that an
  inbound flow arrives through;
- a flow cannot be classified. That fails the guard whatever the change is.

The guard can be overridden. On the command line add
`--override tailscale.link`; in the web interface use the checkbox in the
dialog. Override it only when you have another way into the host.

## Login and logout

`tailscale.node.login` logs the node in with an auth key. Its fields are:

- `authKey`: the auth key. It is a secret. The CLI reads it without echo on a
  terminal, or one line from standard input, and never takes it as a flag.
  Create keys in the [tailnet](tailnet.md) module, or in Tailscale's admin
  console.
- `hostname`: the name the node takes in the tailnet.
- `advertiseTags`: the tags the node advertises, each `tag:<name>`.
- `relogin`: log in again when the node is logged in already. Without it, a
  login on a logged-in node is refused. With it, the guard checks the change.

`tailscale.node.logout` is irreversible: you type the host name, and the guard
checks it as it checks a stop.

## Package

The module installs and updates the `tailscale` package with `apt-get` from
Tailscale's own repository. These actions are long-running: the change is
accepted at once and you follow it as it progresses.

- `tailscale.package.install` adds the repository if it is absent and installs
  the package. It writes Tailscale's signing key, which Darkpanel carries, to
  `/usr/share/keyrings/tailscale-archive-keyring.gpg`, and the repository line
  to `/etc/apt/sources.list.d/tailscale.list`. It is available without
  `tailscaled`. If the package is installed already, it fails with
  `tailscale.package_present`. If either repository file exists and differs
  from what Darkpanel would write, it fails with `tailscale.repo_conflict` and
  writes nothing. Identical files are used, and left alone when you uninstall.
- `tailscale.package.update` updates to the repository's current version. It
  needs the package installed, and refuses a held package with
  `tailscale.package_held`. Release the hold first with
  `tailscale.package.unpin` when a pin set it, or with
  `apt-mark unhold tailscale`.
- `tailscale.package.pin` installs exactly the `version` you give and holds the
  package at it, so ordinary updates leave it alone. Replacing `tailscaled` can
  cut the agent's own link to the controller, so a pin is a transaction that
  rolls itself back. It stages the old version first, installs the new one,
  holds it, and then waits up to 120 seconds for `tailscaled` to answer, the
  node to return to its earlier backend state, and the agent's session to the
  controller to be up. If any step fails, the host goes back to the old version
  and the old hold state, and the pin fails with `tailscale.pin_failed`. The
  rollback runs on the host and needs neither the controller nor the repository.
  The pin needs the package installed.
- `tailscale.package.unpin` restores the hold state that the pin recorded. It
  does not change the installed version.

## Uninstall

The module declares one uninstall option, `package`. Give it with
`--module-option`:

- `--module-option tailscale.package=keep`, the default, leaves the package on
  the host.
- `--module-option tailscale.package=remove` removes the package, releasing its
  hold first, only if Darkpanel installed it. A Tailscale you installed yourself
  stays.

## Troubleshooting

| Code | Cause | What to do |
|---|---|---|
| `tailscale.unavailable` | `tailscaled` does not answer on its socket, or the action needs the package and it is not installed. | Install the package with `tailscale.package.install`, or start `tailscaled`. |
| `tailscale.package_present` | You asked to install, and the package is installed. | Use `tailscale.package.update` to update it. |
| `tailscale.package_held` | You asked to update a package that `apt` holds. | Release the hold with `tailscale.package.unpin` when a pin set it, or with `apt-mark unhold tailscale`, then update. |
| `tailscale.repo_conflict` | The keyring or the repository list exists and is not what Darkpanel writes. | Remove or fix the file the message names, or keep your own repository and install the package yourself. |
| `tailscale.pin_failed` | A step of the pin failed, or the pin could not stage the old version for a rollback. The message says whether the host is back at the old version. | If it is, nothing changed. If the rollback failed too, run `darkpanel change repair <id>` and check the host. |
| `plan.guard_failed` | The guard `tailscale.link` failed. | Read the flow the plan names. Change the plan, or override the guard when you have another way in. |

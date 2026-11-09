# Firewall

The `firewall` module manages UFW, Ubuntu and Debian's firewall, on a host.
It adds and removes rules, sets UFW's defaults and logging, keeps a snapshot
before every change, protects published Docker ports, and shows which of a
host's listening ports each kind of client can reach. Every change comes with
a safety net: the module predicts the result before it applies, refuses a
change that would cut your own access, and reverts the change by itself unless
you confirm it. The module runs in the agent on each host. Its page in the web
interface is `/hosts/<id>/firewall`, with the subpages `snapshots` and
`exposure`.

## Requirements

The host needs these, or the module reports itself unavailable with the
reason:

- UFW installed at `/usr/sbin/ufw`, version 0.35 or later. Darkpanel keeps its
  rules apart from yours with a tag in the rule comment, and comments need
  0.35.
- The `iptables-save` and `iptables-restore` tools of the variant UFW uses,
  legacy or nft. The `ip6tables` tools are optional.
- When the kernel's `nf_tables` module is loaded, the `nft` command from the
  `nftables` package. Without it the host counts as an unsupported
  configuration, because Darkpanel cannot read every rule.

The Docker integration needs more, which "Docker integration" below lists.
Commands for a host take `--host <host>`; you can leave it out when you have
one host.

## Actions and resources

Every action that changes the firewall is revertible: it is undone by the
revert timer unless you confirm it. When you apply a change on the command
line, `--yes` skips the `Apply? [y/N]` question. `--confirm` confirms the
change at once, and `--confirm-timeout` sets the seconds the change waits,
from 10 to 900; "Auto-revert" explains both.

| Name | Command | Kind | Minimum scope | What it does |
|---|---|---|---|---|
| `firewall.status` | `darkpanel firewall status get` | Read | `read` | Shows the configured and loaded state, every rule with its origin, and whether UFW is active, in sync and supported. |
| `firewall.rule.add` | `darkpanel firewall rule add` | Revertible | `operate` | Adds one rule. |
| `firewall.rule.delete` | `darkpanel firewall rule delete` | Revertible | `operate` | Deletes one rule by id. |
| `firewall.rules.apply` | `darkpanel firewall rules apply` | Revertible | `operate` | Adds up to 256 rules and deletes up to 256 in one change. |
| `firewall.state.enable` | `darkpanel firewall state enable` | Revertible | `operate` | Turns UFW on. |
| `firewall.state.disable` | `darkpanel firewall state disable` | Revertible | `operate` | Turns UFW off. |
| `firewall.state.repair` | `darkpanel firewall state repair` | Revertible | `operate` | Brings UFW's loaded state to what its configuration describes, or repairs a change whose revert failed. |
| `firewall.defaults.set` | `darkpanel firewall defaults set` | Revertible | `operate` | Sets the default policies for incoming, outgoing and routed traffic. |
| `firewall.logging.set` | `darkpanel firewall logging set` | Revertible | `operate` | Sets UFW's logging level. |
| `firewall.snapshots` | `darkpanel firewall snapshots get` | Read | `read` | Lists the snapshots, newest first. |
| `firewall.snapshot.compare` | `darkpanel firewall snapshot compare` | Read | `read` | Compares two snapshots, or one with the host now. |
| `firewall.snapshot.restore` | `darkpanel firewall snapshot restore` | Revertible | `operate` | Restores a snapshot. |
| `firewall.docker` | `darkpanel firewall docker get` | Read | `read` | Shows what the Docker integration sees, whether it is on and healthy, and its container rules. |
| `firewall.docker.enable` | `darkpanel firewall docker enable` | Revertible | `operate` | Turns the Docker integration on. |
| `firewall.docker.disable` | `darkpanel firewall docker disable` | Revertible | `operate` | Turns the Docker integration off and removes its chains. |
| `firewall.docker.repair` | `darkpanel firewall docker repair` | Revertible | `operate` | Rewrites the integration's block, loads its chains live, and restarts UFW when the differences reach outside Darkpanel's chains. |
| `firewall.docker.set-sources` | `darkpanel firewall docker set-sources` | Revertible | `operate` | Sets the private sources and the forward exemptions. |
| `firewall.container.allow` | `darkpanel firewall container allow` | Revertible | `operate` | Allows a source to a published container port. |
| `firewall.container.deny` | `darkpanel firewall container deny` | Revertible | `operate` | Denies a source range on an allowed container port. |
| `firewall.container.remove` | `darkpanel firewall container remove` | Revertible | `operate` | Removes an allowance or a deny. |
| `firewall.containers.apply` | `darkpanel firewall containers apply` | Revertible | `operate` | Applies a list of container allowances, denies and removals in one change. |
| `firewall.exposure` | `darkpanel firewall exposure get` | Read | `read` | Lists every listening port with who can reach it. |

## How it works

UFW has two states, and Darkpanel reads both:

- The configured state is what UFW's files say: `/etc/ufw/ufw.conf`,
  `/etc/default/ufw`, the rules in `/etc/ufw/user.rules` and `user6.rules`, and
  the framework files `before*.rules` and `after*.rules`.
- The loaded state is what the kernel is running, read with `iptables-save`
  and `ip6tables-save`, plus the live values of the sysctl keys that UFW
  applies.

A host is in sync when the loaded state is the one UFW would produce from the
configured state. The status shows `inSync`, and lists the chains that differ
when it is false.

Two conditions limit what you can change:

- A host with an unsupported configuration refuses every change with
  `firewall.unsupported_configuration`. That includes an executable
  `before.init` or `after.init` script in `/etc/ufw`, `MANAGE_BUILTINS=yes`,
  rules in the other iptables variant or in native nftables chains, and sysctl
  settings that affect filtering outside the keys Darkpanel knows. The status
  names the reason.
- A host that is out of sync fails the lockout guard for every change, and
  the failure cannot be overridden. The exception is a repair
  (`firewall.state.repair` or `firewall.docker.repair`) when the difference is
  in UFW's and Darkpanel's own chains. A repair plans from the loaded state, so
  it brings the host back in sync.

## Rules

A rule has these fields:

- `action`: `allow`, `deny`, `limit` or `reject`. `limit` applies to incoming
  rules only.
- `direction`: `in`, `out` or `route` for forwarded traffic.
- `proto`: `tcp`, `udp`, `any`, or `ah`, `esp`, `gre`, `ipv6` or `igmp`. The
  last five take no ports, and `ipv6` and `igmp` need family `v4`.
- `from` and `to`: `any`, an address or a CIDR, each with an optional
  `fromPort` or `toPort`: a port, a range `a:b`, or a comma list of at most 15
  multiport slots. Ranges and lists need `tcp` or `udp`.
- `inIface` and `outIface`: an interface name, `inIface` for incoming and
  routed rules and `outIface` for outgoing and routed rules.
- `family`: `v4`, `v6` or `both`. `both` is valid only when `from` and `to`
  are `any`.
- `log`: `off`, `log` or `log-all`.
- `comment`: up to 64 characters from letters, digits, space and `:._/-`.

Darkpanel writes each rule through `ufw`, never into a file directly, and tags
it with the comment `darkpanel:<id>`, followed by your comment. A rule without
the tag is a manual rule. The rules table shows each rule's origin as a badge,
`darkpanel` or `manual`.

To add a rule, put it in a file:

```json
{"rule": {"action": "allow", "direction": "in", "proto": "tcp", "from": "any", "to": "any", "toPort": "443", "family": "both"}}
```

and run:

```sh
darkpanel firewall rule add --host <host> --input rule.json
```

Changing or deleting a manual rule needs your consent. Without it the plan
fails with `firewall.manual_required` and lists the manual rules the change
would touch. Add `--manual` to give the consent. The same applies to any change
that removes manual rules as a side effect, such as a UFW start with
`IPV6=no`, which discards IPv6 rules.

A manual rule that the model cannot express, such as one that uses a protocol
or option outside the list above, appears read-only with its raw tuple.
Darkpanel keeps it through every operation and counts it when it checks your
access.

`firewall.rule.add` adds one rule, `firewall.rule.delete` removes one by its
id as the status lists it, and `firewall.rules.apply` adds and deletes a batch
as one change with one label.

## Settings

- `firewall.state.enable` and `firewall.state.disable` turn UFW on and off.
  Turning it off while the Docker integration is on also removes the
  integration's chains.
- `firewall.state.repair` reloads UFW so the loaded state matches the
  configuration. You also use it to repair a change whose revert failed, as
  "Auto-revert" describes.
- `firewall.defaults.set` sets the default policies for incoming, outgoing
  and routed traffic to `ACCEPT`, `DROP` or `REJECT`. You can set one or all.
  Changing a default policy restarts UFW briefly.
- `firewall.logging.set` sets the logging level to `off`, `low`, `medium`,
  `high` or `full`.

## The lockout guard

Before a change, the plan predicts the loaded state that the change leads to.
It then checks each flow you depend on against that state:

- SSH: a new connection from the address of each SSH session that is open now.
- On the controller's host, the request you are making to the web interface,
  whether it arrives directly or through an exposure such as Nginx Proxy
  Manager or a tunnel.
- On the controller's host, the connections of the agents to the agent
  listener, and each listening port of the controller process (the direct
  web interface, the runtime listener and the agent listener), reached from
  an internet address, or for the runtime listener from the proxy
  container, whether or not a client is connected now. Each connection the
  controller has accepted is checked too. This is what keeps a change made
  with `--local` on that host from cutting the web interface or the agent
  links.
- On an agent's host, the agent's outbound connection to the controller.
- Tailscale's own outbound traffic, when tailscaled runs.

A flow passes only if it passes on every interface it could arrive on. When a
flow would be dropped, rejected or cannot be decided, the plan fails the guard
`firewall.lockout` with the code `plan.guard_failed` and names the flow.

You can override the guard when you know better, for example when you reach
the host another way. On the command line, add `--override firewall.lockout`.
In the web interface, tick the checkbox in the change dialog. You cannot
override it on a host with an unsupported configuration, or on one that is out
of sync, because the prediction is not reliable there.

Tailscale SSH sessions are not firewall flows. The Tailscale module's guard
`tailscale.link` covers them.

After the apply, the module compares the loaded state with the prediction. A
difference fails the change with `firewall.prediction_mismatch` and reverts
it.

## Auto-revert

Every change stays pending until you confirm it. If you do not confirm within
the timeout, the agent reverts it. The default is 60 seconds, and
`--confirm-timeout` sets 10 to 900 seconds. The timer starts before the change
applies, so a hung apply or a restart of the agent still ends in a revert.

- On the command line, `--confirm` confirms the change as soon as it has
  applied. Without it, the change stays pending until its deadline. Run
  `darkpanel change confirm <id>` to keep it, or `darkpanel change revert <id>`
  to undo it now.
- In the web interface, the change dialog shows a countdown with Confirm and
  Revert buttons, and a field for the timeout.

A revert restores the snapshot taken before the change when nothing else
touched UFW in between. When someone edited UFW meanwhile, it undoes only the
change's own effects and leaves the other edits. If a revert cannot undo the
change completely and the host as it stands would cut a flow you depend on, the
module restores the whole snapshot even over those edits, because keeping you
connected comes first.

When a revert cannot finish, the change becomes `needs_repair`. Run
`darkpanel change repair <id>`; it plans `firewall.state.repair`. Its option
`outcome` is `restore`, the default, which returns the host to the snapshot,
or `keep`, which brings the host to the state the change intended.

## Snapshots

The module takes one snapshot before every change. A snapshot holds UFW's
state files, the Docker integration's container rules and settings, and the
integration flag. The first change on a host also pins a snapshot called the
baseline, which is never pruned. Besides the baseline, the module keeps the
newest 50 snapshots of changes you made and the newest 10 of changes the
`firewall.reconcile` task made, plus any that an unconfirmed change or a
change that needs repair still refers to.

- `firewall.snapshots` lists them. The web page `snapshots` shows the list.
- `firewall.snapshot.compare` shows what differs between two snapshots, or
  between one and the host now, as a plan lists changes.
- `firewall.snapshot.restore` restores one by `id`. Its plan lists every
  manual rule the restore would add or remove, and you need `--manual` to give
  your consent. A restore is revertible like any other change.

## Docker integration

UFW alone does not protect ports that Docker publishes. Docker adds its own
rules ahead of UFW's, so a published port is open to the internet whatever your
UFW rules say. The Docker integration closes that gap: it writes a marked
block into `/etc/ufw/after.rules` and `after6.rules`, and the block sends
traffic for published ports through a gate that your rules govern.

### What the integration needs

The status `firewall.docker` reports whether the host qualifies. The
integration is supported when all of this holds:

- Docker is version 28.0 or later and its firewall backend is `iptables`.
- Docker's chains and UFW's chains are in the same iptables variant.
- Docker's protection of published ports in the `raw` table is loaded.
- UFW's default incoming policy is `DROP` or `REJECT`. This holds for as
  long as the integration is on, and the module refuses a change that would
  set it to `ACCEPT`.

Otherwise the integration reports why and refuses to enable with
`firewall.unsupported_integration`. A Docker network that the block cannot
protect, such as one with a routed gateway mode, is listed as unprotected and
makes the integration `degraded`.

### How the block decides

- Established traffic, and traffic that enters from a Docker bridge, passes.
- Traffic from the `privateSources` ranges passes. The defaults are the
  private IPv4 ranges, `100.64.0.0/10` for Tailscale, `fc00::/7` and
  `fe80::/10`. You can change the list, with at most 32 entries.
- Any other new connection to a published port passes only if a binding allows
  it, and only for the sources that binding allows. A published port with no
  binding is closed.
- A new connection that is forwarded to a Docker bridge or to a private
  destination without having been translated for a published port is denied.

A binding is one published port: a container name or a Compose
`project/service`, a host address (all by default), a host port and a protocol.
Each binding has its own policy, so two bindings to the same container port
never share one. The container policy lives in the module, not in your UFW
files, and the module draws the matching rules for the containers that exist
now.

### Turning it on and off

`firewall.docker.enable` writes the block and loads its chains live. It refuses when:

- `DOCKER-USER` holds rules that are not Docker's own `-j RETURN` or the
  block's. The refusal is `firewall.docker_user_in_use` and lists the rules.
  Remove them, or run `firewall.docker.repair` with `discardForeign` and
  `manual` set, which lists each rule it discards. Darkpanel never discards a
  foreign rule by itself.
- The host has a `DNAT` port forward that is not Docker's and no forward
  exemption covers it, because the block's gate would catch that traffic. The
  refusal is `firewall.unsupported_integration`. `firewall.docker` lists a
  proposed exemption for each such rule in `foreignForwards`. Add them with
  `firewall.docker.set-sources`, then enable again.

`firewall.docker.disable` removes the block and then cleans up the chains it
loaded, because deleting the block from the files does not unload them. The
cleanup also runs when you turn UFW off while the integration is on, restore a
snapshot taken without the integration, and uninstall. It refuses with
`firewall.docker_user_in_use` if a foreign rule sits in `DOCKER-USER`. A revert
is never refused for that reason; it puts the foreign rules back where they
were.

The integration stays switched on when you turn UFW off with
`firewall.state.disable`. Turning UFW on again writes the block before UFW
starts.

`firewall.docker.repair` rewrites the block, loads its chains live, and restarts
UFW when the differences reach outside Darkpanel's chains. The status shows
the integration's health: `off`, `ok`, `degraded`, `broken` or `unsupported`,
with the issues. Offer `firewall.docker.repair` when it is `broken`.

### Sources and exemptions

`firewall.docker.set-sources` changes either or both lists:

- `privateSources`: the ranges whose traffic reaches every published port,
  whatever the bindings say. No entry may be wider than `/8` for IPv4 or `/7`
  for IPv6, so `0.0.0.0/0` and `::/0` are refused.
- `forwardExemptions`: the port forwards that are not Docker's, which the
  gate should let through. Each names the pre-translation address and port,
  the translated address and port and the protocol, and optionally the
  interface. A destination inside a Docker network's subnet is refused with
  `firewall.unsupported_integration`.

### Container policy

- `firewall.container.allow` allows a binding, optionally only for the source
  range `from`.
- `firewall.container.deny` denies a source range on a binding that is
  allowed. A deny inside a `privateSources` range is refused, because it could
  never match.
- `firewall.container.remove` removes an allowance or a deny by its id, as
  `firewall.docker` lists it.
- `firewall.containers.apply` applies a list of these operations in one change.
  An empty list realizes the stored rules again against the current
  containers.

The Allow and Deny buttons on a container's page in the Docker module are these
two actions. They are disabled, with the reason beside them, when Docker is
unavailable or the integration is not supported, when the container publishes
no port, and, for Deny, when it has no allowed binding yet.

A binding served by `docker-proxy` for an address family where the container
has no address, such as IPv6 on an IPv4-only network, never crosses `FORWARD`.
UFW's incoming rules govern it like a service on the host, and the allowance
is a tagged `ufw allow in` rule.

### The reconcile task

The background task `firewall.reconcile` keeps the integration in step with
the containers. It wakes on Docker events, such as a container start or die or
a network change, and every 5 minutes. It recomputes the rules from the stored
container policy and the current containers, and applies the difference as
`firewall.containers.apply` or `firewall.docker.repair`, as the actor
`system:firewall.reconcile`. It does nothing while `DOCKER-USER` holds a
foreign rule. Docker objects labelled `darkpanel.module` belong to other
Darkpanel modules, and the firewall never touches them.

## Exposure

`firewall.exposure` lists one row for each listening port: protocol, address,
port and family. A published Docker port appears as its container's binding,
even when `docker-proxy` holds the socket. For each row, the verdict is shown
for each class of source:

- `internet`: addresses outside every `privateSources` range.
- `private`: one entry per `privateSources` range.
- `tailnet`: Tailscale's addresses, arriving on `tailscale0`. The verdict is
  the host firewall's only. The row says "host firewall; tailnet policy not
  evaluated", because the tailnet's own access rules can block traffic earlier.
  When Tailscale's shields-up is on, the class is `closed`.
- `localhost`.

The verdict is one of:

- `open`: every source in the class gets through.
- `partial`: some do. The row lists the allowed sources and the rule that
  decides each.
- `closed`: none do, and the host refuses the connection. A port bound to
  loopback only, or hidden by Tailscale's shields-up, is `closed` as well.
- `filtered`: none do, and the host drops the traffic instead of refusing it,
  so a scanner gets no answer.
- `unknown`: the module cannot decide, for example when a rule matches on
  something it cannot evaluate.

Each class also names the rule that decides it: a UFW rule with its tag, a
line of a framework file, a Docker or tailscaled chain, a line of the block, or
"bound to loopback". The exposure page can show only TCP or UDP, only IPv4 or
IPv6, and only the ports the internet can reach. A host with an unsupported
configuration has no rows, and the page says why.

## Break-glass

These commands run as root on the host itself, through the agent's socket, so
they work when the controller or your network does not.

Roll back the newest firewall change, other than those the reconcile task
made:

```sh
sudo darkpanel firewall rollback --local
```

A pending change is reverted. A confirmed change is undone by restoring the
snapshot taken before it, and the restore is confirmed as soon as it verifies.
`--change <id>` rolls back a specific change, and `--yes` skips the question.

Restore a snapshot by its id. `firewall.snapshots` lists the ids:

```sh
sudo darkpanel firewall snapshots get --local
sudo darkpanel firewall snapshot restore --local --id <id> --confirm
```

If UFW and the kernel are out of step and nothing else works, repair them:

```sh
sudo darkpanel firewall state repair --local
```

## Uninstall

The module declares one uninstall option, `rules`, which decides what happens
to your rules when you uninstall Darkpanel. Give it with `--module-option`:

- `--module-option firewall.rules=remove`, the default, deletes every rule
  Darkpanel tagged. Manual rules stay.
- `--module-option firewall.rules=keep` leaves every rule in place, tags
  included.
- `--module-option firewall.rules=baseline` restores the baseline snapshot,
  which is the firewall as it was before Darkpanel's first change. It logs
  each manual rule it adds or removes. A host with no baseline, because
  Darkpanel never changed its firewall, is left as it is.

In every case the cleanup of the Docker integration's chains runs. If a
foreign rule is in `DOCKER-USER`, the uninstall stops after the rules work with
`firewall.docker_user_in_use`; remove the rule and run the uninstall again.

## Troubleshooting

| Code | Cause | What to do |
|---|---|---|
| `plan.guard_failed` | A guard failed. For the firewall this is `firewall.lockout`: the change would drop a flow you depend on, or the host is out of sync. | Read the flow the plan names. Change the rule so it keeps the flow, or override the guard with `--override firewall.lockout` when you have another way in. On an out-of-sync host, repair it first. |
| `firewall.manual_required` | The change would add, change or remove manual rules. | Check the manual rules the plan lists, then repeat with `--manual`. |
| `firewall.unsupported_configuration` | The host or the configuration the change leads to is outside what Darkpanel can predict. | Fix what the message names, for example remove `before.init`, or install `nftables`, and try again. This is not overridable. |
| `firewall.unsupported_integration` | The Docker integration is not supported on this host, or the change would break its invariants. | Read the reason in `firewall.docker`. It names the Docker version, the backend, the iptables variant, the incoming policy or the network involved. |
| `firewall.docker_user_in_use` | `DOCKER-USER` holds rules that the integration does not own. | Remove the rules, or run `firewall.docker.repair` with `discardForeign` and `manual` set and check the rules the plan lists. |
| `firewall.prediction_mismatch` | After the apply, the loaded state differed from the prediction. The change was reverted. | Look at the change's output for the differing chains. If the host is out of sync, run `darkpanel firewall state repair`. |

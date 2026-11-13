# Panel exposure

## Overview

The `panel` module publishes the Darkpanel web UI itself on a public hostname.
`panel exposure add` does it through [Nginx Proxy Manager](npm.md), with
`--via npm`, or through a [Cloudflare tunnel](cloudflare.md), with
`--via tunnel`. It makes the panel reachable by anyone who can reach that
hostname, so expose it only when you need that.

Logging in over the exposure needs a one-time code (TOTP), as it does anywhere
else. Every page of the web UI shows the banner
`Panel exposed via <npm|tunnel> at <host>` while the exposure exists, with a
link to remove it. The controller opens a listener that admits only the proxy,
and it refuses an unsafe request whose `Origin` header names another site than
the exposure's hostname. The audit log marks every request that came through
the exposure and records both addresses, as `<client> via <proxy>`.

Only one exposure exists at a time. Every panel action needs the `admin` scope.

## Before you start

- The host is the controller's own host. Any other host is refused with
  `panel.wrong_host`.
- Every user has finished TOTP enrolment. If a user is still pending, the add is
  refused with `panel.totp_incomplete`, and the message counts them.
- No exposure exists. The add refuses with `panel.already_exposed` while one
  exists, and also when a Docker network named `darkpanel-panel` already exists
  on the host.
- For `--via npm`, a confirmed public Nginx Proxy Manager instance runs on the
  host. Otherwise the add refuses with `npm.not_public`. NPM requests a Let's
  Encrypt certificate for the hostname, so its DNS points at the host and ports
  80 and 443 reach it.
- For `--via tunnel`, `--tunnel` is the tunnel's ID, a lower-case UUID, and the
  connector of that tunnel runs on the host. If the connector's container does
  not exist, is not the cloudflare module's or is not running, the add refuses
  with `cloudflare.connector_missing`. The hostname must belong to a zone of the
  connected Cloudflare account. `--tunnel` is refused with `--via npm`.
- The hostname is lower case, at most 253 bytes, with at least two labels of
  letters, digits and hyphens that neither start nor end with a hyphen, and its
  last label starts with a letter.
- `--port` is optional. It is a number from 1024 to 65535, and the default is
  9443.

## Use it in the web UI

The page is `/hosts/<id>/panel`. While no exposure exists, the page of the
controller's own host shows a form, and the page of any other host says that the
panel can be exposed only from the controller's own host. The form asks for the
**Route**, which is **Nginx Proxy Manager** or **Cloudflare tunnel**, the
**Public hostname**, the **Tunnel id** when the route is a tunnel, and an
optional **Port**. It tells you that an empty port means 9443, and it accepts a
whole number from 1024 to 65535. **Review** shows the plan.

While an exposure exists, every host's page shows its route, hostname, tunnel,
network, gateway, proxy, port and state, and a **Remove exposure** button. The
state is `open`, `revoked` or `needs_repair`.

The banner on every page links to this page with **Remove the panel exposure**.
It adds a line when the exposure is `revoked`, "The listener is revoked, but the
removal has not finished", and when it is `needs_repair`, "The exposure needs
repair. Remove it again to finish." If the banner cannot read the exposure's
status, it says so, because silence would look like "not exposed".

## Use it from the CLI

Commands reach the controller with an API token that you set up as the
[repository README](../../../README.md#use-the-cli-with-a-token) shows. Each
command below is the generated command of one action, and the [panel API
reference](../../api/panel.md) lists its input and output.

A compensable action asks nothing, and you can undo it with
`darkpanel change undo <id>` unless its row says otherwise. A composite runs
several actions in order and cannot be undone with `change undo`. When a step of
`panel.exposure.add` fails, the engine undoes the finished steps in reverse.
When a step of `panel.exposure.remove` fails, you repair it with
`darkpanel change repair <id>`, which repeats the steps.

| Action | Command | What it does | Asks you |
|---|---|---|---|
| `panel.network.create` | `darkpanel panel network create` | Creates the panel network between the proxy and the panel's listener on a free /29 of 172.31.255.0/24. | Nothing. Undo removes the network while no container is attached. |
| `panel.network.remove` | `darkpanel panel network remove` | Removes the panel network between the proxy and the panel's listener. | Nothing. `change undo` does not apply; `panel.network.create` makes the network again. |
| `panel.listener.open` | `darkpanel panel listener open` | Opens the runtime listener that serves the exposed panel. It runs only as a step of `panel.exposure.add`. | Nothing. Undo revokes and closes the listener and deletes the exposure. |
| `panel.listener.revoke` | `darkpanel panel listener revoke` | Revokes the exposed panel's runtime listener, then closes it. | Nothing. It is never reversed: nothing reopens the listener. |
| `panel.exposure.status` | `darkpanel panel exposure status` | Shows whether the panel is exposed and how. | Nothing; it only reads. |
| `panel.exposure.add` | `darkpanel panel exposure add` | Exposes the panel on the controller's host through Nginx Proxy Manager or a Cloudflare tunnel. | Your confirmation of the firewall rule. |
| `panel.exposure.remove` | `darkpanel panel exposure remove` | Revokes the panel's listener, then removes its proxy host or hostname, attachment, network and firewall rule. | Your confirmation of the firewall change. |

An action that runs on one host takes `--host <id or name>`; you can leave it
out when you have only one host. These are `network create` and `network
remove`. An action whose input names `hostId` takes `--host-id` and runs on the
controller: `listener open` and `exposure add`. `exposure status`,
`exposure remove` and `listener revoke` take no host. `--yes` skips the
`Apply? [y/N]` question. `--local` sends an agent-side command to the agent
socket of the host you are on, as root, when the controller cannot be reached.
`listener revoke` is the exception to `--url`: it talks to the controller socket
and takes `--socket` or `--dev` instead.

```sh
darkpanel panel exposure add --host-id <controller-host-id> --via npm --hostname panel.example.com
darkpanel panel exposure status
darkpanel panel exposure remove
```

`--via tunnel` also needs `--tunnel <tunnel-id>`. `--port` sets the listener's
port.

### What the add does

The add runs these steps in order. It stops at the first one that fails and
undoes the earlier ones.

1. It checks the route: for `npm`, that the host's NPM instance is confirmed
   public; for `tunnel`, that the connector runs on the host.
2. It creates the Docker network `darkpanel-panel`, with the bridge `dp-panel`.
   The subnet is the highest free /29 of `172.31.255.0/24` that overlaps no
   subnet of any Docker network on the host, no route of the host of /8 or
   longer, and no address of the host's interfaces, so a LAN or VPN already on
   that range keeps its addresses. If none is free, the add refuses and names
   what takes the range. The gateway is the first host address of the subnet,
   and the proxy the second.
3. It attaches the proxy, the NPM container or the tunnel's connector, to that
   network at the proxy address.
4. The controller opens a listener on the gateway address and the port. It
   admits only the proxy.
5. For `npm` only, it requests a Let's Encrypt certificate for the hostname.
6. It adds the proxy host, or the tunnel's hostname. The proxy host forwards to
   `https://` and the gateway address and port, with forced SSL and HSTS. The
   tunnel's hostname has the same service, with the verification of the origin's
   certificate turned off.
7. Last, it adds a firewall rule that allows the proxy to reach the gateway's
   port over `dp-panel`. This is the one step you confirm:

```sh
darkpanel change confirm <change-id>
```

If you do not confirm within the firewall's window, 60 seconds by default, the
firewall rule reverts, and the add undoes its earlier steps.

### What the removal does

`panel exposure remove` first revokes the listener. The revocation is permanent:
nothing reopens it, and only a new add exposes the panel again. The removal then
removes the proxy host or the hostname, detaches the proxy from the network,
removes the network, and deletes the firewall rule, which you confirm like the
add's. If a step fails, the exposure stays but is revoked, so the panel is
unreachable through it. The banner then says that the removal has not finished,
or that the exposure needs repair. Run the removal again, or
`darkpanel change repair <change-id>`.

The certificate stays in NPM, and `npm certificate remove` deletes it. Repeated
adds and removals for one hostname count against Let's Encrypt's limit on
duplicate certificates. `panel.not_exposed` refuses a removal when nothing is
exposed.

### Break-glass revocation

If you cannot use the web UI, revoke the listener on the controller's host over
the controller socket, as root:

```sh
sudo darkpanel panel listener revoke
```

This closes the listener at once. Then run `darkpanel panel exposure remove` to
finish the removal. The plan of the removal then has nothing left to revoke.

## What it changes on the host

- The Docker network `darkpanel-panel`, with the label `darkpanel.module=panel`,
  the bridge interface `dp-panel`, IPv6 off, and the subnet and addresses above.
- The proxy's attachment to that network.
- One firewall rule that allows TCP from the proxy address to the gateway
  address and port, coming in on `dp-panel`, labelled `panel exposure` and the
  hostname.
- In the proxy module: the proxy host and its certificate for `npm`, or the
  hostname for `tunnel`.
- A runtime listener inside the controller on the gateway address. It is not a
  file.
- A record of the exposure in the controller's database.

The module owns no file on the host, so `sudo darkpanel uninstall` removes
nothing of it. The network is a Docker object that outlives an uninstall, and
the proxy host, the certificate and the hostname outlive it too. They are
leftovers. Remove the exposure with `panel exposure remove` first.

## When something goes wrong

| Code | What it means here | What you do |
|---|---|---|
| `panel.wrong_host` | The host you named is not the controller's host. | Use the controller's own host. |
| `panel.totp_incomplete` | At least one user has not finished TOTP enrolment. | Have every pending user enrol, then add again. |
| `panel.already_exposed` | An exposure exists, or a Docker network named `darkpanel-panel` exists on the host. | Remove the exposure, or the foreign network. |
| `panel.not_exposed` | There is no exposure to remove or revoke. | Nothing to do. |
| `npm.not_public` | The host has no deployed NPM instance, or it is not confirmed public. | Deploy NPM and confirm that ports 80 and 443 are public; see the NPM page. |
| `cloudflare.connector_missing` | The tunnel's connector does not exist on the host, is not the cloudflare module's, or is not running. | Deploy or start the connector on the controller's host. |
| `request.invalid_input` | An input is wrong: the route, the hostname, the tunnel ID or the port. | The message names the field. Correct it. |
| `docker.owned` | A network named `darkpanel-panel` exists that the panel module did not label, and the removal refuses to delete it. | Remove that network yourself, or rename it. |

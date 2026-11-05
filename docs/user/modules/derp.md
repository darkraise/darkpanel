# DERP servers

## Overview

A DERP server is a custom Tailscale relay region that `derper` serves from one
of your hosts. Tailscale nodes that cannot reach each other directly relay their
traffic through it. The `derp` module installs derper on a host as a systemd
service of its own system user, opens its ports, and publishes the server as a
region in your tailnet's DERP map. The controller does the publishing: it edits
the tailnet policy through the tailnet module, so the tailnet account must be
connected for that step.

Derper starts with `-verify-clients`. It admits only nodes of your tailnet: it
checks each client's node key with the `tailscaled` of its own host, and it
refuses a client when that `tailscaled` does not answer. While the check is on,
no setting makes derper admit a client that `tailscaled` cannot confirm. Give
`verifyClients` as `false` to turn the check off. The plan then shows the
warning "client verification is off: any node that knows the region can relay
through it".

The check works only when `tailscaled` of the DERP host can see every client, so
publishing a region also adds a grant to your policy that lets every node reach
the DERP host's node on `tcp:443`. The grant is addressed to the node's tag.

The deploy pins the host's `tailscaled` to the derper version and holds it, as
the tailscale module's package pin does. The host then takes a Tailscale update
only when a Darkpanel release changes the derper version.

## Before you start

- The host is on the tailnet. `derp.server.install`, `derp.binary.update` and
  `derp.cert.renew` are available only while `tailscaled` of the host answers
  and reports `Running`. If `tailscaled` is in another state, they refuse with
  `derp.not_ready` and name the state; if it does not answer, they refuse with
  `tailscale.unavailable`.
- To publish a region, the tailnet account is connected, and the host's tailnet
  node has at least one tag. The grant above is addressed to the smallest tag of
  the node, and the tailnet's device for the host must carry that tag.
- The ports `80/tcp`, `443/tcp` and `3478/udp` are free on the host. The install
  refuses with `derp.port_in_use` when anything listens on one of them, for
  example [Nginx Proxy Manager](npm.md), and names the port and the program.
  The deploy of Nginx Proxy Manager refuses a host with an installed DERP
  server for the same reason.
- You choose a certificate mode:
  - `letsencrypt`: the host name is a lower-case DNS name of at most 253 bytes
    with at least two labels, and not an IP address. The name's DNS points at the
    host, and port 80 serves the ACME challenge. Derper requests and renews the
    certificate itself.
  - `ip`: the host name is the host's public IP address in canonical form, a
    global unicast address. These are refused: private, loopback, link-local,
    multicast and unspecified addresses, IPv4-mapped IPv6 addresses and
    Tailscale addresses (`100.64.0.0/10` and `fd7a:115c:a1e0::/48`). Derper
    makes a self-signed certificate for the address when it first starts, valid
    for one year. Clients pin it: the DERP map names it as `sha256-raw:`
    followed by the SHA-256 of the certificate, in lower-case hexadecimal.
- A region has an id from 900 to 999, a code (a lower-case letter followed by at
  most 31 lower-case letters, digits or hyphens) and a name of 1 to 64 bytes.

## Use it in the web UI

The DERP page is at `/derp`. Its **Deploy a DERP server** button opens a dialog
with the host, the certificate mode (**Let's Encrypt** or **IP certificate**),
the DNS name or public IP address, and the **Verify clients** checkbox. The
dialog warns when you clear the checkbox, and **Review** shows the plan.

The servers table has these columns:

- **Host**, **Mode**, **Server name** and **Region**, which reads **Not
  published** until a region exists.
- **Version**, with a badge that names the version your release carries when the
  server runs another one.
- **Certificate**, which reads **Renewed by derper** in Let's Encrypt mode and
  the expiry time in IP mode.
- **Health**, a badge of `ok`, `degraded` or `broken` with one line per reason.
  "Health" below says what sets it. A failed check shows as **Last check
  failed**.

Each row has buttons. **Probe** opens the probe panel, **Region** opens the
dialog that publishes a region, **Upgrade** appears when the version differs
from your release's, **Rotate certificate** appears in IP mode, and **Remove**
removes the server. A **Published regions** list under the table has a **Remove
region** button for each region.

The probe panel shows a **Ready to publish** or **Not ready** badge, the region,
and a table of the probes from the controller and from the agents that probed,
each with its TLS and STUN result. It lists the connectivity diagnostics that a
source reported, and a table of the tailnet's nodes with their address, whether
the DERP host's `tailscaled` sees them, their preferred region, and whether a
peer of theirs is relayed through this region now. **Probe again** sends the
probes again. The diagnostics show that the region answers a throwaway node key.
They do not prove that two nodes can relay through it.

The region dialog takes the region id, code and name, and the **Omit default
regions** checkbox. When you check it, an **Acknowledgement** field asks you to
type `nodes will relay only through` followed by the region code. The dialog
warns when the probe has not passed, and the server refuses to publish until it
does.

Every change opens the change dialog with its plan. A change that ends with a
firewall change waits for your confirmation, which "Confirm a change that ends
with a firewall rule" describes.

### Health

The controller reads each host's DERP status every 60 seconds. A server is
`broken` when derper is not running or its certificate has expired. It is
`degraded`, and the page names the reason, when:

- an IP certificate expires within 30 days;
- the last check is older than 10 minutes;
- the host could not be read in the last check;
- the tailnet policy could not be read, so the region may be out of date.

Otherwise it is `ok`.

## Use it from the CLI

Commands reach the controller with an API token that you set up as the
[repository README](../../../README.md#use-the-cli-with-a-token) shows. Each
command below is the generated command of one action, and the [DERP API
reference](../../api/derp.md) lists its input and output.

A compensable action asks nothing, and you can undo it with
`darkpanel change undo <id>` unless its row says otherwise. An irreversible
action asks you to type a phrase; none of the DERP actions is irreversible. A
composite runs several actions in order and cannot be undone with `change undo`.
When a step of an adding composite fails, the engine undoes the finished steps
in reverse. When a step of a removing composite fails, you repair it with
`darkpanel change repair <id>`, which repeats the steps.

| Action | Command | What it does | Asks you |
|---|---|---|---|
| `derp.server.install` | `darkpanel derp server install` | Installs derper on this host as a systemd service run by its own system user. | Nothing. Undo with `change undo`. |
| `derp.binary.update` | `darkpanel derp binary update` | Replaces derper with the version this agent's release carries, rolling back when it is not healthy. | Nothing. Undo with `change undo`. |
| `derp.server.status` | `darkpanel derp server status` | Reports the DERP server on this host and the host's place on the tailnet. | Nothing; it only reads. |
| `derp.node.probe` | `darkpanel derp node probe` | Probes a DERP server over TLS and STUN from this host. | Nothing; it only reads. |
| `derp.server.uninstall` | `darkpanel derp server uninstall` | Removes the DERP server from this host. | Nothing. `change undo` does not apply; `derp.server.install` puts a server back. |
| `derp.cert.renew` | `darkpanel derp cert renew` | Replaces the DERP server's self-signed certificate with a new one. | Nothing. Undo with `change undo`. |
| `derp.server.probe` | `darkpanel derp server probe` | Probes a DERP server's TLS and STUN reachability from the controller and chosen agents, and shows which nodes use its region. | Nothing; it only reads. |
| `derp.server.list` | `darkpanel derp server list` | Lists the DERP servers the controller found, with their health. | Nothing; it only reads. |
| `derp.server.deploy` | `darkpanel derp server deploy` | Pins `tailscaled`, installs derper and opens its ports on a host. | Your confirmation of the firewall rules. |
| `derp.server.upgrade` | `darkpanel derp server upgrade` | Pins `tailscaled`, then replaces derper; between the two steps the versions differ and the region may refuse clients. | Nothing. |
| `derp.cert.rotate` | `darkpanel derp cert rotate` | Renews the IP certificate, then publishes its pin; between the two steps clients holding the old pin cannot use the region. | Nothing. |
| `derp.region.set` | `darkpanel derp region set` | Publishes a DERP server as a region of the tailnet's DERP map. | Nothing. |
| `derp.region.remove` | `darkpanel derp region remove` | Removes a region from the tailnet's DERP map. | Nothing. |
| `derp.server.remove` | `darkpanel derp server remove` | Withdraws a DERP server's region, uninstalls derper, unpins `tailscaled` and closes the server's ports on a host. | Your confirmation of the firewall change. |

An action that runs on one host takes `--host <id or name>`; you can leave it
out when you have only one host. These are the agent-side actions: `install`,
`binary update`, `server status`, `server uninstall`, `cert renew` and `node
probe`. An action whose input names `hostId` takes `--host-id` and runs on the
controller. `--yes` skips the `Apply? [y/N]` question. `--local` sends an
agent-side command to the agent socket of the host you are on, as root, when the
controller cannot be reached.

```sh
darkpanel derp server deploy --host-id <host-id> --mode ip --host-name 203.0.113.10
darkpanel derp server probe --host-id <host-id>
darkpanel derp region set --host-id <host-id> --region-id 900 \
  --region-code example --region-name "Example relay"
```

The CLI has no flag for `verifyClients`. To turn verification off, put
`"verifyClients": false` in a JSON document and pass it with `--input`, or with
`--input -` on standard input. Flags override its fields.

### Confirm a change that ends with a firewall rule

`derp.server.deploy` and `derp.server.remove` end with a firewall change. The
change waits in the state `awaiting_confirmation` until you confirm it:

```sh
darkpanel change confirm <change-id>
```

If you do not confirm within the firewall's window, 60 seconds by default, the
firewall change reverts by itself. A deploy then undoes its earlier steps. A
removal stops in `needs_repair`, and `darkpanel change repair <change-id>`
finishes it.

### Deploy a server and publish its region

1. Run `derp server deploy`, then confirm it as above. The deploy pins
   `tailscaled`, installs derper, and adds three firewall rules. It does not
   publish the region.
2. Run `derp server probe`. It probes `/derp/probe` and STUN from the
   controller and from other agents. `--agents` (repeatable, at most 8) names
   the hosts that probe; without it, the other hosts probe.
3. Run `derp region set`. It is refused with `derp.not_ready` until the newest
   deploy of that host is completed and the probe passes: the controller's TLS
   and STUN probes pass, and so do those of at least one agent that is not the
   DERP host. A fleet with no such agent relies on the controller alone, and the
   plan carries a warning. The host must also report the mode and host name the
   deploy installed.
4. `--omit-default-regions` drops Tailscale's default regions. It is accepted
   when another custom region's server passes the probe, or when you type the
   acknowledgement `nodes will relay only through <region code>` with
   `--acknowledge`. Otherwise it is refused with `request.invalid_input`.

The region holds one node with the host name, port 443, STUN port 3478 and, in
IP mode, the pin as its `CertName`. A region id that already belongs to another
host name is refused with `derp.region_taken`.

`derp region remove` deletes the region from the policy. When it was the last
custom region, it also clears `omitDefaultRegions`, so nodes always keep a
relay, and it removes the visibility grant of that server. If it cannot read the
server's tag, it leaves the grant in place and warns you.

### Upgrade and rotate

`derp server upgrade` pins `tailscaled`, then replaces derper with the version
of your release. The update keeps the old binary as `derper.prev`. It waits up
to 60 seconds for the health check: derper's unit is active, and the TLS probe,
the STUN probe and `tailscaled` answer on the host. If a step fails, it
restores the old binary.

`derp cert rotate` works in IP mode only; in Let's Encrypt mode derper renews
the certificate itself and the command is refused with `request.invalid_input`.
It sets the certificate and key aside under `.prev` names, restarts derper so
that it writes a new pair, and waits up to 30 seconds for it. When the server
publishes a region, it then publishes the new pin. Connections through the
server drop at the restart. Between the renewal and the policy change, clients
that hold the old pin cannot use the region.

When the server publishes a region, both commands first check that the DERP
host keeps a management path that does not use that region: its link to the
controller does not run over the tailnet, or the tailnet keeps Tailscale's
default regions, or another custom region's server passes the probe. If none
holds, they refuse with `derp.no_fallback`.

## What it changes on the host

- The system user `darkpanel-derp`.
- The binary `/usr/local/lib/darkpanel/derper`, mode 0755. An update stages the
  new binary as `derper.new` and keeps the one it replaces as `derper.prev`.
- The unit file `/etc/systemd/system/darkpanel-derper.service`, enabled and
  started. It runs derper as `darkpanel-derp` with only the capability to bind
  low ports, and restarts it after failures.
- The directory `/var/lib/darkpanel/derp`, owned by the user. Its `certs`
  directory holds derper's certificates: its ACME cache in Let's Encrypt
  mode, and in IP mode the certificate and key files of the address with the
  `.prev` copies that a renewal sets aside. Its key file `derper.key`, mode
  0600, is written by derper itself and kept by every upgrade.
- Three firewall rules labelled `derp`, which allow `80/tcp`, `443/tcp` and
  `3478/udp` from any address.
- A hold on the host's `tailscaled` package at the derper version.
- In your tailnet policy: the region, the `omitDefaultRegions` flag if you set
  it, and the visibility grant.

`derp.server.remove` withdraws the region when the server publishes one and the
tailnet account is connected. It then removes the unit, the unit file, the
binary with its `.new` and `.prev` copies, the directory with the node key and
the certificates, the user and the record of the server. Last, it releases the
hold and deletes the three rules. A later install makes a new node key.

`sudo darkpanel uninstall` stops the unit and removes the unit file, the binary,
the directory and the user, as the host's ownership manifest lists them. It does
not withdraw your policy entries, and with the default tailscale option it
leaves the package hold in place; release it with `apt-mark unhold tailscale`.
The firewall module's uninstall option decides what happens to the three rules.
Anything of the list above that outlives an uninstall is a leftover.

## When something goes wrong

| Code | What it means here | What you do |
|---|---|---|
| `derp.port_in_use` | A program listens on `80/tcp`, `443/tcp` or `3478/udp` of the host. The message names the port and the program. | Stop that program or use another host. |
| `derp.not_ready` | The server or the host is not ready. Among other causes: the host is not on the tailnet; no DERP server is installed or recorded for the host; the deploy is not confirmed; a probe failed; the host's node has no tag, or the device does not carry it; derper has not written its certificate yet. | Read the message. Join the host to the tailnet, confirm the deploy, or fix the probe, then plan again. |
| `derp.region_taken` | The region id already belongs to a node of another host name in the policy. | Choose another id from 900 to 999, or remove the other region. |
| `derp.no_fallback` | The DERP host's link depends on its own region and no other region is reachable. | Add a second region with a server that passes the probe, or clear `omitDefaultRegions`, then try again. |
| `derp.verify_failed` | A derper step failed its check: during an install, the unit did not become active within 30 seconds or, in IP mode, derper wrote no certificate within 30 seconds; during a renewal, derper wrote no new certificate within 30 seconds; or a step of a binary update failed, such as its health check. | Read the message. "derper is back at" a version means the update rolled back and nothing needs repair. "derper needs repair" means the rollback failed too: run `darkpanel change repair <id>`. After a failed install, `darkpanel change undo <id>` removes what it made. |
| `request.invalid_input` | An input is invalid. For an install, for example: the mode or host name is invalid; a DERP server is already installed on the host; or the user `darkpanel-derp`, the binary, the unit file or the service directory already exists and is not Darkpanel's. | Correct the input or remove the foreign object. |
| `tailscale.unavailable` | `tailscaled` does not answer on its socket. | Start `tailscaled` on the host. |

# Cloudflare Tunnel

## Overview

The `cloudflare` module manages Cloudflare tunnels that are remotely managed:
the controller keeps a tunnel's configuration at Cloudflare, not in a file on a
host. The module creates tunnels, runs a connector container for each tunnel on
a host, and publishes public hostnames through them. No inbound port opens on
any host, because each connector dials out to Cloudflare.

Darkpanel names the tunnels it creates `darkpanel-<change id>`. The connector of
a tunnel runs as the container `darkpanel-cloudflared-<tunnel id>`, from a
cloudflared image pinned by its digest. It reads its token from a file that is
mounted into the container, never from its environment or its command line. The
token is write-only: you give it to Darkpanel when you connect the account, and
no API call and no page shows it again.

## Before you start

- A Cloudflare account, and its account ID: 32 lower-case hexadecimal digits.
- An API token for that account with these permissions, as the Cloudflare
  dashboard names them:
  - **Cloudflare Tunnel: Edit** on the account.
  - **Zone Read** on the zones you will use.
  - **DNS: Edit** on the zones you will use.

  The token is at most 512 bytes and holds no space or control character.
- Docker on each host that runs a connector.

Connecting probes what the token can read, which are the tunnels of the account
and its zones, and reports each of those capabilities as `ok` or `missing`. It
reports the two write capabilities, `tunnels.write` and `dns.write`, as
`unverified`, because a probe would have to write. They become known only when a
write succeeds.

Cloudflare offers no protection against two clients that change one tunnel at
once, so you must not edit a tunnel that Darkpanel created in the Cloudflare
dashboard or with another client. Darkpanel keeps a hash of the configuration it
last wrote. When it finds a different configuration before a write, it reports
drift with `cloud.conflict` and writes nothing, until you accept that
configuration with `cloudflare tunnel adopt`. A check every 5 minutes looks for
drift in the tunnels that have hostnames, and for the health of each hostname.
The Tunnels and Hostnames pages show the result.

This protects you only from changes made before Darkpanel reads. A write that
another client makes between Darkpanel's read and its own write is overwritten
without trace. The DNS record delete and the configuration write are
read-then-write checks, not compare-and-swap, and the window is one API round
trip. The plans of `cloudflare hostname add` and `remove` say so.

## Use it in the web UI

The page is `/cloudflare`. Four links at its top open its pages.

- **Account**: a form for the **Account ID** and the **API token**, with the
  permissions the token needs listed above the **Connect** button. The token
  field is cleared when the dialog closes. Once an account is connected, the page
  shows its ID, when it was connected and the **Token capabilities**. A
  **Disconnect** button and an **Abandon pending cleanup** checkbox end the
  connection.
- **Tunnels**: a table of each tunnel's name, status, connections, number of
  hostnames, **Configuration** (**In sync**, or **Changed outside Darkpanel**)
  and creation time. **Create tunnel** adds one. A row has **Delete**, and
  **Accept configuration** while the tunnel has drifted.
- **Connectors**: choose a host, and the page lists the connectors on it with
  their container and state, and a **Stop** button for each. **Deploy
  connector** runs a connector for a tunnel, or for a new tunnel it creates
  first.
- **Hostnames**: a table of each hostname, tunnel, service, state, health and
  last check, with a **Remove** button. The state is the step of an add or a
  removal that the hostname has reached, and **active** when it is done. The
  health is **ok**, **rule missing**, **record missing**, **record changed** or
  **unknown**. **Add hostname** takes the tunnel, the hostname, the service,
  and a checkbox that turns off the verification of the origin's TLS
  certificate.

The page of a container in the docker module has an **Expose via tunnel**
action. It asks for the tunnel, the network, the port and the hostname.

## Use it from the CLI

Commands reach the controller with an API token that you set up as the
[repository README](../../../README.md#use-the-cli-with-a-token) shows. Each
command below is the generated command of one action, and the [Cloudflare API
reference](../../api/cloudflare.md) lists its input and output.

A compensable action asks nothing, and you can undo it with
`darkpanel change undo <id>` unless its row says otherwise. An irreversible
action asks you to type a phrase, which the last column names; `--confirmation`
gives it on the command line. A composite runs several actions in order and
cannot be undone with `change undo`. When a step of an adding composite fails,
the engine undoes the finished steps in reverse. When a step of a removing
composite fails, you repair it with `darkpanel change repair <id>`, which
repeats the steps.

| Action | Command | What it does | Asks you |
|---|---|---|---|
| `cloudflare.account.connect` | `darkpanel cloudflare account connect` | Connects the Cloudflare account with an API token. | Nothing. Undo deletes the stored account, unless cleanup is pending. |
| `cloudflare.account.disconnect` | `darkpanel cloudflare account disconnect` | Disconnects the Cloudflare account and deletes the stored token. | You type the account ID. |
| `cloudflare.account.get` | `darkpanel cloudflare account get` | Shows the connected Cloudflare account. | Nothing; it only reads. |
| `cloudflare.tunnel.create` | `darkpanel cloudflare tunnel create` | Creates a remotely managed Cloudflare tunnel. | Nothing. Undo deletes the row and then the tunnel, each only while nothing uses it. |
| `cloudflare.tunnel.delete` | `darkpanel cloudflare tunnel delete` | Deletes a managed Cloudflare tunnel. | You type the tunnel's name. |
| `cloudflare.tunnel.list` | `darkpanel cloudflare tunnel list` | Lists the managed Cloudflare tunnels. | Nothing; it only reads. |
| `cloudflare.tunnel.adopt` | `darkpanel cloudflare tunnel adopt` | Accepts a Cloudflare tunnel's current configuration as expected. | Nothing. Undo restores the earlier hash and drift mark. |
| `cloudflare.hostname.add` | `darkpanel cloudflare hostname add` | Routes a public hostname to a service through a managed Cloudflare tunnel. | Nothing. Undo with `change undo`. |
| `cloudflare.hostname.remove` | `darkpanel cloudflare hostname remove` | Removes a public hostname's DNS record and ingress rule. | Nothing. `change undo` does not apply; `cloudflare.hostname.add` puts a hostname back. |
| `cloudflare.hostname.list` | `darkpanel cloudflare hostname list` | Lists the public hostnames of the managed Cloudflare tunnels. | Nothing; it only reads. |
| `cloudflare.connector.run` | `darkpanel cloudflare connector run` | Runs the cloudflared connector of a tunnel in a Darkpanel-owned container on this host. | Nothing. Undo with `change undo`. |
| `cloudflare.connector.stop` | `darkpanel cloudflare connector stop` | Stops the cloudflared connector of a tunnel and removes its container, token file and record from this host. | Nothing. `change undo` does not apply; `cloudflare.connector.run` runs a connector again. |
| `cloudflare.connector.list` | `darkpanel cloudflare connector list` | Lists the Cloudflare tunnel connectors of this host and the state of their containers. | Nothing; it only reads. |
| `cloudflare.network.attach` | `darkpanel cloudflare network attach` | Joins the tunnel's connector to a Docker network and records the claimant that needs it. | Nothing. Undo with `change undo`. |
| `cloudflare.network.detach` | `darkpanel cloudflare network detach` | Releases a claimant's network claims on the tunnel's connector. | Nothing. Undo with `change undo`. |
| `cloudflare.connector.deploy` | `darkpanel cloudflare connector deploy` | Runs a tunnel's connector on a host, creating the tunnel first when none is named. | Nothing. |
| `cloudflare.expose.container` | `darkpanel cloudflare expose container` | Attaches a tunnel's connector to a container's network and routes a hostname of the tunnel to the container. | Nothing. |
| `cloudflare.expose.remove` | `darkpanel cloudflare expose remove` | Removes an exposed container's hostname and the network claims of its exposure. | Nothing. |

An action that runs on one host takes `--host <id or name>`; you can leave it
out when you have only one host. These are the agent-side actions: `connector
run`, `connector stop`, `connector list`, `network attach` and `network detach`.
An action whose input names `hostId` takes `--host-id` and runs on the
controller: `connector deploy`, `expose container` and `expose remove`. The
others run on the controller and take no host. `--yes` skips the
`Apply? [y/N]` question. `--local` sends an agent-side command to the agent
socket of the host you are on, as root, when the controller cannot be reached.
On the controller, `--tunnel` takes a tunnel's ID or its name. The agent-side
commands take the tunnel's ID, which is a lower-case UUID.

The API token is a secret, so `account connect` has no flag for it. It reads the
token without echo on a terminal, or one line from standard input:

```sh
darkpanel cloudflare account connect --account-id <account-id> < <token-file>
darkpanel cloudflare connector deploy --host-id <host-id>
darkpanel cloudflare hostname add --tunnel <tunnel> --hostname app.example.com \
  --service http://203.0.113.10:8080
darkpanel cloudflare expose container --host-id <host-id> --tunnel <tunnel> \
  --container web --network app --port 8080 --hostname web.example.com
```

### Connect and disconnect

A second `account connect` while an account is connected is refused with
`request.invalid_input`. To change the token, disconnect first.

`account disconnect` is irreversible and deletes the stored token. It leaves the
tunnels, the hostnames and the connectors in place, but while no account is
connected every change that needs Cloudflare refuses with `cloud.not_connected`.
It refuses with `cloud.cleanup_pending` while Darkpanel still has a cleanup
pending on the account, and lists each item: a hostname that is not in the phase
`active`, which means an add or a removal is in progress, and an unresolved
effect of a Cloudflare change. Finish them first, or give them up with
`--abandon-cleanup`.

### Tunnels and connectors

`tunnel delete` refuses with `cloud.conflict` while hostnames are routed through
the tunnel, in any phase, and while connectors are connected to Cloudflare.
Remove the hostnames and stop the connectors first. It then deletes the tunnel
at Cloudflare, and Darkpanel's record of it.

`connector deploy` creates a tunnel when you name none, reads the tunnel's token
inside the controller, and runs the connector on the host with it. No command
shows the token. `connector run` takes the token as a secret, from a terminal or
standard input, and refuses a connector already deployed for the tunnel and a
container of the connector's name that already exists. The connector container
restarts unless it is stopped.

`connector stop` removes the container, its token file and its record. It
refuses with `docker.in_use` while an exposure still has the connector attached
to a network, and with `docker.owned` for a container of that name that the
module did not label. Remove the exposures with `expose remove`, or release a
claim whose hostname is already gone with `network detach`.

### Public hostnames

`hostname add` takes a hostname and a service.

- The hostname is lower case: letters, digits, hyphens and dots, at most 253
  characters, with at least two labels of 1 to 63 characters that neither start
  nor end with a hyphen.
- The service is `http_status:` and a code from 100 to 599, or an `http`,
  `https`, `tcp` or `ssh` URL with a host and a port. It has no user, path,
  query or fragment. `--no-tlsverify` turns off the check of the origin's
  certificate.

It refuses with `request.invalid_input` a hostname that Darkpanel already
manages, a hostname that no zone of the account holds, a hostname that the
tunnel already has a rule for, and a hostname for which its zone already has a
DNS record. It checks for drift first.

The add then inserts the rule before the catch-all `http_status:404` that every
tunnel configuration ends with, and creates a proxied CNAME to
`<tunnel id>.cfargotunnel.com`, with the comment `darkpanel:<change id>`.
`hostname remove` deletes only the record and the rule that Darkpanel recorded.
It refuses with `cloud.conflict` when the record no longer matches what Darkpanel
made, when the hostname is still being added, and when the tunnel's
configuration has drifted.

### Expose a container

`expose container` attaches the connector of the tunnel to the container's
network, then adds a hostname whose service is `http://<container>:<port>`.
Unlike the Nginx Proxy Manager route, it requests no certificate.

The connector must run on the host you name; otherwise the change refuses with
`cloudflare.connector_missing`. That code also means that the connector's
container does not exist. The connector reaches the container by name, so the
container must be on the network you name, and that network must be a
user-defined bridge network. The default `bridge` network, `host`, `none` and
networks of other drivers do not qualify. Otherwise the change refuses with
`cloudflare.unsupported_target`: connect the container to a user-defined network
first. Attachments are claim based, as for [Nginx Proxy Manager](npm.md):
removing one exposure never detaches a network that another exposure uses.

`cloudflare.reserved` refuses a hostname whose service is an address in the panel
network `172.31.255.0/24`, or a name that resolves into it, or a name whose
lookup fails for another reason than "not found", because then the controller
cannot tell. Only the [panel exposure](panel.md) may add such a hostname, and
only it may remove one whose service is an address in that network.

## What it changes on the host

- The connector container `darkpanel-cloudflared-<tunnel id>`, with the label
  `darkpanel.module=cloudflare` and a restart policy of `unless-stopped`. It
  joins the Docker networks of the exposures that need it.
- One token file for each connector in
  `/var/lib/darkpanel/agent/cloudflare-tokens`, named `<tunnel id>.token`, mode
  0400, owned by the user and group of the image. The container mounts it
  read-only.
- Records in the agent's database: each connector, with a sealed copy of its
  token, and the network claims.

At Cloudflare, outside the host, the module creates the tunnels, the ingress
rules of their configuration and the CNAME records.

`sudo darkpanel uninstall` removes the token directory, as the host's ownership
manifest lists it. It does not remove Docker objects, so the connector containers
outlive it, and they are leftovers. Stop each connector with `cloudflare
connector stop` first. Uninstall does not touch the tunnels and records at
Cloudflare either; delete the hostnames and tunnels with the commands above
before you uninstall.

## When something goes wrong

| Code | What it means here | What you do |
|---|---|---|
| `cloud.not_connected` | No Cloudflare account is connected. | Connect one with `cloudflare account connect`. |
| `cloud.auth_failed` | Cloudflare answered 401: it rejected the token. | Disconnect, then connect again with a valid token. |
| `cloud.permission_missing` | Cloudflare answered 403: the token lacks a permission this change needs. | Add the permission to the token in Cloudflare, or disconnect and connect with a token that has it. |
| `cloud.rate_limited` | Cloudflare answered 429. | Wait, then plan again. |
| `cloud.unavailable` | Cloudflare could not be reached or answered with an error, for a read or a delete, after retries. | Try again later. |
| `cloud.conflict` | The state at Cloudflare is not what Darkpanel expects: a tunnel's configuration changed outside Darkpanel; a tunnel you delete still has hostnames or connectors; a record or rule that an undo or a removal would change was changed by someone else; or Cloudflare answered 409 or 412. | Read the message. For drift, check the configuration at Cloudflare, then accept it with `cloudflare tunnel adopt`. |
| `cloud.uncertain_outcome` | A write failed in a way that leaves its result unknown: it may still have committed. | Wait for the commit window of 10 minutes, then run `darkpanel change effects <id>` and `darkpanel change resolve`. |
| `cloud.cleanup_pending` | Darkpanel still has cleanup pending on the account. The refusal lists each item. | Finish the items, or add `--abandon-cleanup` to give them up. |
| `cloudflare.reserved` | The service of a hostname is in the panel network, and the change is not the panel exposure's. | Use the panel exposure, or another service. |
| `cloudflare.unsupported_target` | The network is not a user-defined bridge network, or the container is not on it. | Connect the container to a user-defined network. |
| `cloudflare.connector_missing` | No connector of the tunnel is deployed on the host, or its container does not exist. For the panel exposure, it also means that the container is not the module's or is not running. | Run `cloudflare connector deploy` for that host. |
| `request.invalid_input` | An input is wrong: for example the account ID, the token, a hostname, a service, or a second account. | The message names the field. Correct it. |
| `docker.in_use` | An exposure still has the connector attached to a network. | Remove the exposure first. |

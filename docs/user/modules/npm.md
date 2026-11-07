# Nginx Proxy Manager

## Overview

The `npm` module runs Nginx Proxy Manager (NPM) on one of your hosts and manages
its proxy hosts, access lists and certificates. You use it to publish a
container on a public name behind a Let's Encrypt certificate, and it is the
route by which the [panel exposure](panel.md) publishes Darkpanel itself.

The instance runs in the container `darkpanel-npm`, from the image this release
pins, on the Docker network `darkpanel-npm`. The volume `darkpanel-npm-data`
holds NPM's database and configuration, and the volume
`darkpanel-npm-letsencrypt` holds its certificates. Ports 80 and 443 are
published on all addresses. The admin API is published on `127.0.0.1:81` only.
The container's configuration has no environment, command or entrypoint, so it
holds no secret.

The deploy creates the NPM administrator with the `acmeEmail` you give, in lower
case, and a random password. The email is also the contact that NPM gives Let's
Encrypt. The password is stored sealed on the host. It never appears in the
container's environment, in the agent's logs or in the log stream of the docker
module. `darkpanel npm admin reveal-password` shows it once, to an admin.

For Let's Encrypt to validate a name and for visitors to connect, ports 80 and
443 must reach the host from the internet. Darkpanel calls the instance public when you have confirmed it, or
when the firewall module's Docker integration allows the container's ports 80
and 443 from any source. Changes that publish something need a public instance
and refuse with `npm.not_public` otherwise.

## Before you start

- Docker 28.0 or later, with the protection of published ports in the raw table
  of iptables that Docker loads from that version. Without either, the module
  reports itself unavailable on the host and `npm.instance.deploy` refuses with
  `module.unavailable` and the reason.
- Ports 80 and 443 are free on the host, and no DERP server is installed on it.
  The deploy refuses with `derp.port_in_use` when a program listens on either
  port, and names the program, or when the [DERP server](derp.md) of the host
  is installed, even if it is stopped. A DERP install refuses a host where NPM
  holds port 80 or 443 for the same reason.
- No Docker network, volume or container of the names above exists. If one
  does, the deploy refuses with `docker.in_use`.
- The `acmeEmail` is one bare address, without a display name, whose domain is
  a name Let's Encrypt can register. The deploy refuses an IP address as the
  domain, a domain without a dot, and these reserved names and any name under
  them: `invalid`, `localhost`, `local`, `internal`, `lan`, `home`, `corp`,
  `test`, `example`, `home.arpa`, `example.com`, `example.net` and
  `example.org`. The refusal is `request.invalid_input`.
- Public reachability, which depends on the firewall module's Docker
  integration of the host:
  - With the integration on, `npm instance deploy-public` allows the container's
    ports 80 and 443 as its last step. That step waits for your confirmation, so
    check from outside that the ports answer before you confirm.
  - With the integration off, the ports are already reachable if your network
    allows them. After you check that, `npm instance confirm-public` records it.

## Use it in the web UI

The page is `/hosts/<id>/npm`, the Nginx Proxy Manager tab of a host. A warning
titled **Admin interface exposed** shows at its top while the admin interface is
published. The same warning shows on every other page of the web UI, with a link
to this page.

The page has four tabs.

- **Instance** shows the deploy form while no instance exists. The form asks for
  the **Let's Encrypt email** and tells you whether the Docker integration is on,
  so you know whether the deploy opens the ports. **Plan** shows the plan of
  `npm.instance.deploy-public`. Once an instance exists, the tab lists its
  status, whether its API answers, its image and the image this release pins,
  the admin email, whether it is public, and its admin interface. It warns while
  public access is not confirmed. Its buttons are **Confirm public** (until
  confirmed), **Upgrade**, **Remove instance**, **Reveal admin password**,
  and **Expose admin interface** or **Remove admin exposure**. A badge says
  **Upgrade available** when your release pins another image. **Reveal admin
  password** opens a dialog that shows the email and the password once; **Hide
  password** closes it.
- **Proxy hosts** lists the ID, domains, target, certificate, access list,
  options and status of each. **Add proxy host** asks for the domains, the
  scheme, the forward host and port, an optional certificate ID and access list
  ID, and the **Force SSL**, **HSTS** and **Websockets** options. Each row has
  **Edit** and **Remove**.
- **Access lists** lists the ID, name, whether any or all rules must match, the
  rules and the status. **Add access list** and **Remove** change them.
- **Certificates** lists the ID, name, domains, expiry and status. **Request
  certificate** and **Remove** change them.

The status column can show a badge that reads `Reserved:` and the action that
owns the object. The module refuses to change a reserved object, and its
buttons are disabled. **Unclaimed Darkpanel object** marks an object that matches a creation
that was cut off and that you resolved as not made, because NPM may have made
it anyway.

The page of a container in the docker module has an **Expose** action. It asks
for the network, the port, the domains and an optional access list, then shows
the plan of `npm.expose.container`.

## Use it from the CLI

Commands reach the controller with an API token that you set up as the
[repository README](../../../README.md#use-the-cli-with-a-token) shows. Each
command below is the generated command of one action, and the [NPM API
reference](../../api/npm.md) lists its input and output.

A compensable action asks nothing, and you can undo it with
`darkpanel change undo <id>`. An irreversible action asks you to type a phrase,
which the last column names; `--confirmation` gives it on the command line. A
composite runs several actions in order and cannot be undone with
`change undo`. When a step of an adding composite fails, the engine undoes the
finished steps in reverse. When a step of a removing composite fails, you repair
it with `darkpanel change repair <id>`, which repeats the steps.

| Action | Command | What it does | Asks you |
|---|---|---|---|
| `npm.instance.deploy` | `darkpanel npm instance deploy` | Deploys Nginx Proxy Manager in a Darkpanel-owned container and creates its administrator. | Nothing. Undo removes the container, both volumes and the network. |
| `npm.instance.status` | `darkpanel npm instance status` | Shows the Nginx Proxy Manager instance of this host and whether it runs and answers. | Nothing; it only reads. |
| `npm.instance.confirm-public` | `darkpanel npm instance confirm-public` | Records that ports 80 and 443 of this host are reachable from the internet. | Nothing. Undo clears the mark. |
| `npm.instance.remove` | `darkpanel npm instance remove` | Removes the Nginx Proxy Manager instance and its data. | You type `darkpanel-npm`. |
| `npm.instance.upgrade` | `darkpanel npm instance upgrade` | Recreates the Nginx Proxy Manager container from the pinned image, archiving its volumes first. | Nothing. Undo restores the original from the archives. |
| `npm.network.attach` | `darkpanel npm network attach` | Attaches Nginx Proxy Manager to a network. | Nothing. Undo with `change undo`. |
| `npm.network.detach` | `darkpanel npm network detach` | Releases an attachment claim. | Nothing. Undo with `change undo`. |
| `npm.proxyhost.add` | `darkpanel npm proxyhost add` | Adds a proxy host. | Nothing. Undo deletes it. |
| `npm.proxyhost.update` | `darkpanel npm proxyhost update` | Changes the settings of a proxy host. | Nothing. Undo with `change undo`. |
| `npm.proxyhost.remove` | `darkpanel npm proxyhost remove` | Deletes a proxy host. | Nothing. Undo with `change undo`. |
| `npm.proxyhost.list` | `darkpanel npm proxyhost list` | Lists the proxy hosts of Nginx Proxy Manager. | Nothing; it only reads. |
| `npm.accesslist.add` | `darkpanel npm accesslist add` | Creates an IP access list in Nginx Proxy Manager. | Nothing. Undo with `change undo`. |
| `npm.accesslist.remove` | `darkpanel npm accesslist remove` | Deletes an access list from Nginx Proxy Manager. | Nothing. Undo with `change undo`. |
| `npm.accesslist.list` | `darkpanel npm accesslist list` | Lists the access lists of Nginx Proxy Manager. | Nothing; it only reads. |
| `npm.certificate.request` | `darkpanel npm certificate request` | Requests a Let's Encrypt certificate through Nginx Proxy Manager. | Nothing. Undo deletes it. |
| `npm.certificate.remove` | `darkpanel npm certificate remove` | Deletes a certificate from Nginx Proxy Manager. | You type the certificate's name, or its ID when it has none. |
| `npm.certificate.list` | `darkpanel npm certificate list` | Lists the certificates of Nginx Proxy Manager. | Nothing; it only reads. |
| `npm.admin.reveal-password` | `darkpanel npm admin reveal-password` | Reveals the Nginx Proxy Manager administrator's password. | Nothing; it only reads, needs the `admin` scope and shows the password once. |
| `npm.instance.list` | `darkpanel npm instance list` | Lists the Nginx Proxy Manager instance status of every host. | Nothing; it only reads. |
| `npm.instance.deploy-public` | `darkpanel npm instance deploy-public` | Deploys Nginx Proxy Manager and, when the host's Docker firewall integration is on, opens ports 80 and 443 for it. | Your confirmation of the firewall change, when the integration is on. |
| `npm.expose.container` | `darkpanel npm expose container` | Attaches the proxy to a container's network, requests a certificate and adds a proxy host that forwards to the container. | Nothing. |
| `npm.admin.expose` | `darkpanel npm admin expose` | Publishes Nginx Proxy Manager's admin UI over HTTPS on a hostname, with a certificate, forced SSL and HSTS. | Nothing. |
| `npm.expose.remove` | `darkpanel npm expose remove` | Removes an exposed container's proxy host and its network claims. | Nothing. |
| `npm.admin.unexpose` | `darkpanel npm admin unexpose` | Removes the proxy host that publishes Nginx Proxy Manager's admin UI; its certificate stays. | Nothing. |

An action that runs on one host takes `--host <id or name>`; you can leave it
out when you have only one host. These are the agent-side actions, which are all
the `instance` actions except `list` and `deploy-public`, plus `network`,
`proxyhost`, `accesslist`, `certificate` and `admin reveal-password`. An action
whose input names `hostId` takes `--host-id` and runs on the controller:
`deploy-public`, `expose container`, `expose remove`, `admin expose` and `admin
unexpose`. `--yes` skips the `Apply? [y/N]` question. `--local` sends an
agent-side command to the agent socket of the host you are on, as root, when the
controller cannot be reached. Input that is a list of objects, such as the
`clients` of an access list, has no flag; give it with `--input`.

```sh
darkpanel npm instance deploy-public --host-id <host-id> --acme-email <acme-email> --integration
darkpanel npm instance status --host <host>
darkpanel npm expose container --host-id <host-id> --container web --network app \
  --port 8080 --domains app.example.com
darkpanel npm admin reveal-password --host <host>
```

`--integration` says that the host's Docker firewall integration is on. Give it
only when it is. The change refuses with `plan.state_changed` if the state of the
integration differs from what you gave.

### Deploy

`npm instance deploy` refuses a bad `acmeEmail`, a host that already has an
instance, an existing Docker object of the module's names, a program that
listens on port 80 or 443, and an installed DERP server. It then pulls the pinned
image if the host lacks it, creates the network and the volumes, starts the
container, waits up to 120 seconds for NPM to answer, creates the administrator
and proves that its login works.

If the administrator cannot be created for another reason, the change fails
with `npm.setup_failed`: the container runs and the administrator may exist. Run
`darkpanel change undo <id>`. The undo removes the container, both volumes and
the network, and forgets the instance. Then deploy again.

If NPM already has an administrator when the deploy reaches that step, or
refuses to create the first one, someone else created it first. The deploy then
removes the container, both volumes and the network it created, so that no
instance keeps running under another administrator, and the change fails with
`npm.setup_failed`. The message says whether the removal succeeded. Find out who
can reach port 81 on the host, then deploy again. Do not undo the failed change:
what it created is already gone, so the undo ends in `needs_repair` and holds the
change's locks. If you undid it, run `darkpanel change effects <id>`, then
`darkpanel change resolve <id> accept --effect <n>` for each effect it lists as
unresolved.

`npm instance deploy-public` is the checked form that the web UI uses. Its first
step checks that the host runs Docker 28.0 or later with the raw-table
protection, and that the integration is as you planned. The next step deploys.
With the integration on, the last step allows the container's ports 80 and 443
from any source and waits for your confirmation:

```sh
darkpanel change confirm <change-id>
```

If the firewall's window passes, 60 seconds by default, the firewall change
reverts and the composite undoes the deploy.

### Expose a container

`npm expose container` needs a public instance, and refuses with `npm.not_public`
otherwise. It then runs these steps:

1. It attaches NPM to the container's network, with a claim named after the
   smallest of the domains.
2. It requests a certificate for the domains.
3. It adds a proxy host that forwards to `http://<container>:<port>`, with forced
   SSL and HSTS, and with websockets and an access list if you give them.

NPM reaches the container by its name, so the container must be connected to the
network you name, and that network must be a user-defined bridge network. The
default `bridge` network, `host`, `none` and networks of other drivers do not
qualify. Otherwise the change refuses with `npm.unsupported_target`: connect the
container to a user-defined network first.

Attachments are claim based. Each exposure records a claim on the network. When
you remove an exposure, NPM leaves the network only when no other claim remains
and NPM was not already on it before the first claim. Removing one exposure
never detaches a network that another exposure uses.

`npm expose remove --host-id <host-id> --domain <domain>` removes the proxy host
and releases the claims. It does not delete the certificate; `npm certificate
remove` does.

### Manage proxy hosts, access lists and certificates

- A proxy host refuses a domain that already has a proxy host, a certificate ID
  or access list ID that does not exist, and, for example, forced SSL without a
  certificate or HSTS without forced SSL. `npm proxyhost remove` takes `--id` or
  `--domain`.
- An access list needs at least one client rule. Each rule is an IP address or a
  CIDR prefix without host bits, with the directive `allow` or `deny`. NPM names
  the list `darkpanel-<change id>-<number>`. Removing a list that proxy hosts use
  is refused with `request.invalid_input`.
- `npm certificate request` orders a certificate from Let's Encrypt, validated
  over HTTP on port 80. Among other things, it refuses an empty list of domains,
  more than 100 domains, a wildcard name, a name with one label, and a name
  listed twice. It waits up to 10 minutes for the order. `npm certificate
  remove` is irreversible and refuses a certificate that proxy hosts use.
- A proxy host that points at the local host or at NPM itself would let a visitor
  reach the admin interface or the panel. The module therefore refuses with
  `npm.reserved` a forward host that is `localhost`, `ip6-localhost`,
  `ip6-loopback`, or a loopback or unspecified address in any form that nginx or
  glibc reads as one, such as `127.1`; the NPM container's own address or one of
  its names, with port 81; or an address in the panel network `darkpanel-panel`
  or its gateway, while that network exists. The admin exposure may use the
  first two kinds and the panel exposure the last, because they own them.
- The admin exposure and the panel exposure reserve the proxy host and the
  certificate they create. While the exposure exists, `npm.reserved` refuses to
  change or delete them. Removing the exposure through its own removal releases
  them, and the certificate then stays until `npm certificate remove` deletes
  it. Proxy hosts and certificates that `npm expose container` creates are not
  reserved. `npm instance remove` also refuses while any exposure reserves an
  object, and names the exposures.

### Admin interface

`npm admin expose` is off by default. It publishes NPM's own admin interface on a
hostname, behind a certificate, with forced SSL and HSTS, and an optional access
list. It needs a public instance. While it is on, every page of the web UI shows
the warning **Admin interface exposed**. `npm admin unexpose` removes the proxy
host and leaves the certificate, which only `npm certificate remove` deletes.

### Upgrade

`npm instance upgrade` recreates the container from the image this release pins
and keeps both volumes. It stops the container and archives each volume to
`/var/lib/darkpanel/agent/npm-backups`, then recreates the container and waits
up to 120 seconds for its API. It keeps the three newest sets of archives, and
any set that an unfinished upgrade still needs. A volume whose archive would
exceed 256 MiB makes the upgrade fail.

If a step fails, the upgrade restores the original container and volumes from
the archives. When the new container's API does not answer in time, the change
fails with `npm.unhealthy`. A cut-off upgrade is rolled back when the agent
starts again. `darkpanel change undo <id>` restores a finished upgrade,
provided the archives still match the checksums recorded for them.

### Changes that become unresolved

When an NPM call is cut off before Darkpanel knows its result, the change can
end in `needs_repair` with an unresolved effect. The call may still commit for a
while: 2 minutes for a proxy host, an access list or an Engine call, and 10
minutes for a certificate, whose ACME order NPM may still complete. Until that
window ends, `darkpanel change resolve` refuses. Then run
`darkpanel change effects <id>` to see the effect, and `darkpanel change
resolve` to accept what you find or to reconcile the record. A certificate that
matches the requested names is never taken as the change's own, because NPM may
reuse a certificate, so you decide.

## What it changes on the host

- The Docker network `darkpanel-npm`, the volumes `darkpanel-npm-data` and
  `darkpanel-npm-letsencrypt`, and the container `darkpanel-npm`. Each carries
  the label `darkpanel.module=npm`, so the docker module refuses to change them.
- With the Docker integration on, the allowances for the container's ports 80
  and 443 that `npm instance deploy-public` adds through the firewall module.
- The directory `/var/lib/darkpanel/agent/npm-backups`, with two archives named
  after the change for each upgrade, `<change id>-data.tar` and
  `<change id>-letsencrypt.tar`, in lower case.
- Records in the agent's database: the instance, its sealed administrator
  password, the network claims and the reserved objects.

`sudo darkpanel uninstall` removes the backup directory, as the host's ownership
manifest lists it, but it does not remove Docker objects. The container, the
network and the volumes outlive it, and they are leftovers. The firewall
module's uninstall option decides what happens to the allowances. Remove the
instance first with `npm instance remove`, which deletes the container, both
volumes and the network, and the stored password. It does not delete the
archives or the firewall allowances. It refuses while an exposure reserves an
object, while a container other than NPM uses the network or a volume, and when
an object of those names lacks the module's label.

## When something goes wrong

| Code | What it means here | What you do |
|---|---|---|
| `npm.not_public` | The instance is not deployed, or is not confirmed public: the confirmation is missing, and the Docker integration does not allow ports 80 and 443 from any source. | Open the ports, check them from outside, then run `npm instance confirm-public`. |
| `npm.unsupported_target` | The container is not on a user-defined bridge network that you named. | Connect the container to a user-defined network, then plan again. |
| `npm.reserved` | The change would touch an object that the admin or panel exposure owns, or point a proxy host at the local host, NPM's admin port or the panel network. `npm instance remove` gives the same code while an exposure reserves objects. | Change the object through its exposure, or remove the exposure first. |
| `npm.setup_failed` | The administrator could not be created: NPM did not answer within 120 seconds, already had an administrator, or did not create the administrator or accept its login. A deploy that found another administrator has already removed what it created. | Run `darkpanel change undo <id>`, which removes the container, both volumes and the network, then deploy again. If the deploy found another administrator, do not undo: deploy again, as the Deploy section says. |
| `npm.unhealthy` | The instance does not answer as expected. Among other causes: its container is missing or stopped, its API is unreachable, NPM refused the stored administrator's login, or an upgrade's new container did not answer. | Read the message. Run `npm instance status`, start the container with the docker module if it stopped, and for a failed upgrade check that the original was restored. |
| `derp.port_in_use` | A program listens on port 80 or 443, or a DERP server is installed on the host. | Stop the program, remove the DERP server, or use another host. |
| `module.unavailable` | The host runs Docker older than 28.0, or the raw-table protection is not loaded. | Update Docker or load its protection, then plan again. |
| `docker.in_use` | A Docker object of the module's names exists before a deploy, or another container uses the network or a volume you remove. | Remove or rename the object, or disconnect the container the message names. |
| `request.invalid_input` | An input is wrong: for example the `acmeEmail`, a domain, a rule of an access list, an instance that already exists, or an object that proxy hosts still use. | The message names the field. Correct it. |
| `plan.state_changed` | The Docker firewall integration changed between your plan and the apply. | Plan again with `--integration` set as the host is now. |

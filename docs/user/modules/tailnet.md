# Tailnet

The `tailnet` module manages your Tailscale tailnet from the controller: the
devices on it, the auth keys that let new devices join, and the access policy
file. It connects to Tailscale's API with an OAuth client that you create, so
it needs no agent on any host and runs in the controller. A controller has one
tailnet. Its page in the web interface is `/tailnet`, with the account, devices,
approvals, auth keys and a policy editor with validation and a diff. Other
modules, such as DERP, change the policy through it. The [tailscale](tailscale.md)
module manages the node on one host.

## Requirements

- A tailnet, and an OAuth client for it, which you create in Tailscale's admin
  console. The client needs these scopes, and Darkpanel uses each for the
  actions named beside it:
  - `devices:core`: authorize, expire and delete devices, and list them.
  - `devices:routes`: approve a device's routes and exit node.
  - `auth_keys`: create auth keys and list the ones Darkpanel requested.
  - `policy_file`: read, validate and write the policy file. Tailscale also
    requires `devices:posture_attributes` for this scope.
- Tags on the client. Tailscale requires an OAuth client with `devices:core`
  or `auth_keys` to have tags, and every auth key it creates carries those tags
  or tags they own in the policy's `tagOwners`.
- A controller that can reach `api.tailscale.com`.

## Actions and resources

A compensable action can be undone afterwards with `darkpanel change undo <id>`,
and the table says what the undo does. An irreversible action cannot be undone,
and its plan asks you to type a confirmation phrase, which the table names;
`--confirmation <phrase>` gives it on the command line. For both, `--yes` skips
the `Apply? [y/N]` question. None of the actions has a revert timer. The
commands run on the controller, so they take no `--host` and no `--local`.

| Name | Command | Kind | Minimum scope | What it does |
|---|---|---|---|---|
| `tailnet.account.connect` | `darkpanel tailnet account connect` | Undone by an inverse | `admin` | Connects the tailnet with an OAuth client. Undo deletes the stored credentials, once no cleanup is pending. |
| `tailnet.account.get` | `darkpanel tailnet account get` | Read | `read` | Shows the connected account and its capabilities. |
| `tailnet.account.disconnect` | `darkpanel tailnet account disconnect` | Irreversible | `admin` | Deletes the stored credentials. You type the tailnet name. |
| `tailnet.device.list` | `darkpanel tailnet device list` | Read | `read` | Lists the tailnet's devices. |
| `tailnet.device.authorize` | `darkpanel tailnet device authorize` | Undone by an inverse | `admin` | Authorizes a device to join. Undo deauthorizes it, if this change authorized it. |
| `tailnet.device.set-routes` | `darkpanel tailnet device set-routes` | Undone by an inverse | `admin` | Sets the routes and exit node a device is approved to serve. Undo sets the earlier list. |
| `tailnet.device.expire` | `darkpanel tailnet device expire` | Irreversible | `admin` | Expires a device's node key so that it must sign in again. You type the device name. |
| `tailnet.device.delete` | `darkpanel tailnet device delete` | Irreversible | `admin` | Removes a device from the tailnet. You type the device name. |
| `tailnet.authkey.create` | `darkpanel tailnet authkey create` | Undone by an inverse | `admin` | Creates a tagged auth key. Undo deletes the key. |
| `tailnet.authkey.list` | `darkpanel tailnet authkey list` | Read | `read` | Lists the auth keys Darkpanel requested. |
| `tailnet.policy.get` | `darkpanel tailnet policy get` | Read | `read` | Reads the policy file with its comments and its ETag. |
| `tailnet.policy.set` | `darkpanel tailnet policy set` | Undone by an inverse | `admin` | Replaces the policy file. Undo restores the earlier text while the policy is unchanged since. |
| `tailnet.policy.patch` | `darkpanel tailnet policy patch` | Undone by an inverse | `admin` | Edits the policy file with typed edits. Undo works like `tailnet.policy.set`. |
| `tailnet.node.join` | `darkpanel tailnet node join` | Composite | `admin` | Joins an enrolled host to the tailnet. A composite runs several actions in order, and undoes them in reverse if one fails. |

## Connect your tailnet

`tailnet.account.connect` takes these fields:

- `tailnet`: the name of your tailnet.
- `clientId`: the OAuth client's id.
- `clientSecret`: the client's secret. It is a secret. The CLI reads it without
  echo on a terminal, or one line from standard input, and never takes it as a
  flag. Darkpanel stores it sealed.
- `tagsAllowed`: the tags the client may assign, each `tag:<name>`. At least
  one.

The plan fetches a token and probes what the client can do. It reads the list
of devices, the list of keys and the policy file, and shows each capability's
state:

- `ok`: the read worked.
- `missing`: the read failed, and the state's detail gives the error code, for
  example `cloud.permission_missing` when the client lacks the scope.
- `unverified`: a write capability, which the connect never tests, because
  testing would write. It becomes known only when a write works.

A connect with credentials that Tailscale rejects fails. A second connect while
a tailnet is connected fails with `cloud.conflict`; disconnect first.

`tailnet.account.get` shows the stored account and the capabilities from the
connect. It does not ask Tailscale again. To probe again after you change the
client's scopes, disconnect and connect.

`tailnet.account.disconnect` deletes the stored credentials and cannot be
undone. You type the tailnet name. It leaves the devices and keys in the
tailnet alone. If a cleanup in Tailscale is still pending, such as an auth key
that Darkpanel must delete, the disconnect is refused with
`cloud.cleanup_pending` until the cleanup is done. Add `abandonCleanup` to
disconnect anyway and leave those keys in Tailscale.

## Devices

`tailnet.device.list` shows each device's name, host name, operating system,
user, addresses, tags, whether it is authorized, when it was last seen, when
its key expires and the routes it advertises and has approved. A device that
advertises `0.0.0.0/0` and `::/0` is an exit node, and the list says whether
both routes are approved.

- `tailnet.device.authorize` authorizes a device to join the tailnet. Undo
  deauthorizes it, if this change authorized it.
- `tailnet.device.set-routes` sets the full list of routes a device may serve.
  The list replaces the earlier one. An exit node is approved by listing both
  `0.0.0.0/0` and `::/0`. Undo sets the list that was approved before.
- `tailnet.device.expire` expires the device's node key. The device must
  authenticate again. You type the device name. It cannot be undone.
- `tailnet.device.delete` removes the device from the tailnet. You type the
  device name. It cannot be undone.

## Auth keys

An auth key lets a new device join. `tailnet.authkey.create` takes these
fields:

- `tags`: the tags the key gives its device, each within `tagsAllowed`. At
  least one is needed.
- `reusable`: whether more than one device can use the key.
- `ephemeral`: whether the device is removed when it goes offline.
- `preauthorized`: whether the device joins without being authorized.
- `expirySeconds`: how long the key is valid, from 1 to 7776000 seconds. The
  default is 86400, one day.

The key is shown once, when you create it. Copy it then; Darkpanel does not
show it again. The key's description in Tailscale is `darkpanel-` and the id of
the change, so you can recognize it. Undo deletes the key. `tailnet.authkey.list`
lists the keys Darkpanel requested, with their state and expiry, but never
their values.

## The policy file

The policy file is Tailscale's access policy, a HuJSON document: JSON with
comments and trailing commas.

- `tailnet.policy.get` reads the file with its comments and its ETag, which is
  Tailscale's version marker for the file.
- `tailnet.policy.set` replaces the file. Its input is the full HuJSON text in
  `text`. The plan reads the current file and its ETag, asks Tailscale to
  validate your text, and shows a line diff. The apply writes the text with the
  ETag it read, so an edit made elsewhere in the meantime makes the apply fail
  with `tailnet.policy_conflict` instead of overwriting it. Text that Tailscale
  rejects fails the plan with `tailnet.policy_invalid`.
- Undo of a set or a patch writes the earlier text back, again with the ETag.
  It works only while the policy is the one your change wrote. If someone
  changed it since, the undo fails with `tailnet.policy_conflict` and the change
  needs repair, so that a later edit is never erased.
- `tailnet.policy.patch` makes small, typed edits and keeps the comments and
  everything it does not touch. Other modules use it. Its input is a list,
  `edits`. Each edit has an `op`, a `path` that is a JSON Pointer to an object
  member or an array, and a `value`:
  - `set` sets an object member to `value`. Setting a member to the value it has
    changes nothing.
  - `delete` removes an object member. Removing one that is absent changes
    nothing.
  - `addItem` appends `value` to an array, unless an equal element is already
    there.
  - `removeItem` removes every element equal to `value`.

  A path that goes through an array index is refused with
  `request.invalid_input`, as are other edit kinds. Planning the same edits
  again after they have been applied gives an empty diff.

An example of the input of `tailnet.policy.patch`:

```json
{
  "edits": [
    {"op": "addItem", "path": "/tagOwners/tag:server", "value": "group:admins"},
    {"op": "set", "path": "/derpMap/OmitDefaultRegions", "value": false}
  ]
}
```

Give it with `--input <file>`.

## Join a host to the tailnet

A new host normally reaches the controller's agent listener only over loopback
or the tailnet. To have a host join the tailnet first, create it with a join:

```sh
darkpanel host create --name <name> --tailnet-join --tags <tag>
```

The controller must be on the tailnet itself, and the tags must be within
`tagsAllowed`. The controller creates a one-use, pre-authorized auth key with
those tags and an expiry of one hour, the same as the enrollment token's. It
puts the key in the bootstrap bundle that the command prints, together with the
enrollment token and the controller's tailnet address. The bundle is a
secret, shown once.

On the new host, run `install.sh --role agent` and give it the bundle, as
"Install Darkpanel" describes for a token. The installer installs Tailscale,
logs the node in with the key, waits up to 60 seconds for a tailnet address,
and then enrolls the host over the tailnet. If any step fails, the host stays
un-enrolled and the installer prints the reason. Run it again with a fresh
bundle to retry. The key is used up when the host enrolls. If the token expires
unused, or you delete the host first, the controller deletes the key.

For a host that is enrolled already and reachable, `tailnet.node.join` joins it
as a retry. Its input is `hostId`, `tags` and, with `install`, an install of
Tailscale first. It creates a one-use pre-authorized key and logs the node in
with `tailscale.node.login`. If a step fails, it undoes the earlier ones in
reverse.

## Troubleshooting

| Code | Cause | What to do |
|---|---|---|
| `tailnet.policy_conflict` | The policy file changed in Tailscale since your plan read it, or since your change wrote it, so an undo cannot restore the earlier text. | Read the policy again, with `tailnet.policy.get`, and plan your change against it. After a failed undo, review the change and repair it. |
| `tailnet.policy_invalid` | Tailscale rejected the text: it does not parse, or it fails its own tests, or it raises warnings. The message is Tailscale's. | Fix the text the message points at, and plan again. |
| `cloud.not_connected` | No tailnet is connected. | Run `tailnet.account.connect`. |
| `cloud.permission_missing` | The OAuth client lacks the scope the action needs. | Add the scope to the client in Tailscale's admin console, then disconnect and connect again. |

A capability shown as `missing` in the account means the same as
`cloud.permission_missing`: the client could not do that read. Add the scope
and connect again. A capability shown as `unverified` is normal after a
connect. It becomes known when the first write works, and fails with
`cloud.permission_missing` if the client lacks the scope.

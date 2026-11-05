# tailscale API

Back to the [API reference](README.md).

## tailscale.latency

Read tailscale.latency

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/tailscale/latency` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `regions` | array of object | yes | Each DERP region, sorted by region id. |
| `regions[].code` | string | yes | The DERP region code. |
| `regions[].https` | object | yes | The DERP HTTPS probe latency to the region. |
| `regions[].https.available` | boolean | yes | Whether a latency was measured. |
| `regions[].https.medianMs` | number | yes | The median round trip in milliseconds; 0 when unavailable. |
| `regions[].https.reason` | string | yes | Why no latency was measured; empty when available. |
| `regions[].name` | string | yes | The DERP region name. |
| `regions[].regionId` | integer | yes | The DERP region id. |
| `regions[].stun` | object | yes | The STUN latency to the region. |
| `regions[].stun.available` | boolean | yes | Whether a latency was measured. |
| `regions[].stun.medianMs` | number | yes | The median round trip in milliseconds; 0 when unavailable. |
| `regions[].stun.reason` | string | yes | Why no latency was measured; empty when available. |

## tailscale.node.login v1

Log the node in to a tailnet with an auth key.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/tailscale.node.login/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/tailscale.node.login/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `advertiseTags` | array of string | no | The ACL tags the node advertises, each tag:\<name\>. |
| `authKey` | string | yes | The Tailscale auth key to log in with. Secret. |
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `hostname` | string | no | The host name the node takes in the tailnet. |
| `overrides` | array of string | no |  |
| `relogin` | boolean | no | Whether to log in again when the node is logged in already. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `addresses` | array of string | yes | The node's Tailscale addresses, IPv4 first. |
| `backendState` | string | yes | The backend state after the login. |
| `dnsName` | string | yes | The node's MagicDNS name, without the trailing dot. |

## tailscale.node.logout v1

Log the node out of its tailnet.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/tailscale.node.logout/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/tailscale.node.logout/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `backendState` | string | yes | The backend state after the logout. |

## tailscale.package.install v1

Install tailscale from Tailscale's apt repository, adding the repository when it is absent.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/tailscale.package.install/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/tailscale.package.install/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `version` | string | yes | The installed tailscale version. |

## tailscale.package.pin v1

Install exactly one tailscale version and hold it, rolling back when the node does not come back.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/tailscale.package.pin/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/tailscale.package.pin/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `version` | string | yes | The tailscale version to install and hold. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `priorHeld` | boolean | yes | Whether the package was held before the pin. |
| `priorVersion` | string | yes | The tailscale version before the pin. |
| `version` | string | yes | The tailscale version now installed and held. |

## tailscale.package.unpin v1

Release the pin of the tailscale package and restore the hold state it replaced.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/tailscale.package.unpin/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/tailscale.package.unpin/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `held` | boolean | yes | Whether the package is held now: the pin's prior hold state. |
| `version` | string | yes | The installed tailscale version, unchanged. |

## tailscale.package.update v1

Update the tailscale package to the repository's current version.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/tailscale.package.update/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/tailscale.package.update/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `from` | string | yes | The tailscale version before the update. |
| `to` | string | yes | The tailscale version after the update. |

## tailscale.prefs.repair v1

Repair a change to the node's Tailscale preferences.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/tailscale.prefs.repair/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/tailscale.prefs.repair/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `outcome` | string | no | restore (the default) puts back the preferences from before the repaired change; keep applies the values the change set. |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `prefs` | object | yes | The repaired preferences. |
| `prefs.acceptRoutes` | boolean or null | no |  |
| `prefs.advertiseExitNode` | boolean or null | no |  |
| `prefs.advertiseRoutes` | array of string or null | no |  |
| `prefs.exitNode` | string or null | no |  |
| `prefs.runSSH` | boolean or null | no |  |
| `prefs.shieldsUp` | boolean or null | no |  |
| `prefs.wantRunning` | boolean or null | no |  |
| `repairOutcome` | string | yes | What the repair did: restored or kept. |

## tailscale.prefs.set v1

Set the node's Tailscale preferences.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/tailscale.prefs.set/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/tailscale.prefs.set/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `acceptRoutes` | boolean or null | no |  |
| `advertiseExitNode` | boolean or null | no |  |
| `advertiseRoutes` | array of string or null | no |  |
| `exitNode` | string or null | no |  |
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `overrides` | array of string | no |  |
| `runSSH` | boolean or null | no |  |
| `shieldsUp` | boolean or null | no |  |
| `wantRunning` | boolean or null | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `prefs` | object | yes | The values now, for exactly the fields the input set. |
| `prefs.acceptRoutes` | boolean or null | no |  |
| `prefs.advertiseExitNode` | boolean or null | no |  |
| `prefs.advertiseRoutes` | array of string or null | no |  |
| `prefs.exitNode` | string or null | no |  |
| `prefs.runSSH` | boolean or null | no |  |
| `prefs.shieldsUp` | boolean or null | no |  |
| `prefs.wantRunning` | boolean or null | no |  |

## tailscale.status

Read tailscale.status

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/tailscale/status` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `backendState` | string | yes | The backend state: NoState, NeedsLogin, NeedsMachineAuth, Stopped, Starting or Running. |
| `health` | array of string | yes | The health warnings tailscaled reports. |
| `magicDNSSuffix` | string | yes | The tailnet's MagicDNS suffix. |
| `peers` | array of object | yes | The tailnet's other nodes, sorted by DNS name, then id. |
| `peers[].addresses` | array of string | yes | The node's Tailscale addresses, IPv4 first. |
| `peers[].connection` | string | yes | How this node reaches the peer: direct, derp, peer-relay or none. |
| `peers[].dnsName` | string | yes | The node's MagicDNS name, without the trailing dot. |
| `peers[].endpoint` | string | yes | The UDP endpoint in use for a direct connection; empty otherwise. |
| `peers[].exitNode` | boolean | yes | Whether the peer is this node's exit node. |
| `peers[].exitNodeOption` | boolean | yes | Whether the peer offers itself as an exit node. |
| `peers[].hostName` | string | yes | The node's host name. |
| `peers[].id` | string | yes | The node's stable id. |
| `peers[].lastHandshake` | string (date-time) or null | yes |  |
| `peers[].online` | boolean | yes | Whether the peer is connected to the coordination server. |
| `peers[].os` | string | yes | The node's operating system. |
| `peers[].region` | string | yes | The DERP region code for a DERP connection; empty otherwise. |
| `peers[].tags` | array of string | yes | The node's ACL tags. |
| `preferredDerp` | string | yes | The region code of the node's home DERP; empty when none. |
| `prefs` | object | yes | The node's current preferences, every field set. |
| `prefs.acceptRoutes` | boolean or null | no |  |
| `prefs.advertiseExitNode` | boolean or null | no |  |
| `prefs.advertiseRoutes` | array of string or null | no |  |
| `prefs.exitNode` | string or null | no |  |
| `prefs.runSSH` | boolean or null | no |  |
| `prefs.shieldsUp` | boolean or null | no |  |
| `prefs.wantRunning` | boolean or null | no |  |
| `runSSH` | boolean | yes | Whether the node runs Tailscale SSH. |
| `self` | object or null | yes |  |
| `tailnet` | string | yes | The tailnet's name; empty when the node belongs to none. |
| `version` | string | yes | The tailscaled version. |

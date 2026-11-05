# cloudflare API

Back to the [API reference](README.md).

## cloudflare.account.connect v1

Connect the Cloudflare account with an API token.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/cloudflare.account.connect/v1/plan` |
| apply | `POST /api/v1/actions/cloudflare.account.connect/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `accountId` | string | yes |  |
| `apiToken` | string | yes | Secret. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `accountId` | string | yes |  |
| `capabilities` | array of object | yes |  |
| `capabilities[].detail` | string | no |  |
| `capabilities[].name` | string | yes |  |
| `capabilities[].state` | string | yes |  |
| `connectedAt` | string (date-time) | yes |  |

## cloudflare.account.disconnect v1

Disconnect the Cloudflare account and delete the stored token.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/cloudflare.account.disconnect/v1/plan` |
| apply | `POST /api/v1/actions/cloudflare.account.disconnect/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `abandonCleanup` | boolean | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `abandoned` | array of string | yes |  |

## cloudflare.account.get v1

Show the connected Cloudflare account.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/cloudflare.account.get/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `account` | object or null | no |  |
| `connected` | boolean | yes |  |

## cloudflare.connector.deploy v1

Run a tunnel's connector on a host, creating the tunnel first when none is named.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/cloudflare.connector.deploy/v1/plan` |
| apply | `POST /api/v1/actions/cloudflare.connector.deploy/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes |  |
| `tunnel` | string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `connector` | object | yes |  |
| `connector.container` | string | yes |  |
| `connector.containerId` | string | yes |  |
| `connector.state` | string | yes |  |
| `connector.tunnel` | string | yes |  |
| `created` | boolean | yes |  |
| `tunnel` | string | yes |  |

## cloudflare.connector.list v1

List the Cloudflare tunnel connectors of this host and the state of their containers.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/cloudflare.connector.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `connectors` | array of object | yes |  |
| `connectors[].container` | string | yes |  |
| `connectors[].containerId` | string | yes |  |
| `connectors[].state` | string | yes |  |
| `connectors[].tunnel` | string | yes |  |

## cloudflare.connector.run v1

Run the cloudflared connector of a tunnel in a Darkpanel-owned container on this host.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/cloudflare.connector.run/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/cloudflare.connector.run/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `token` | string | yes | Secret. |
| `tunnel` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `container` | string | yes |  |
| `containerId` | string | yes |  |
| `state` | string | yes |  |
| `tunnel` | string | yes |  |

## cloudflare.connector.stop v1

Stop the cloudflared connector of a tunnel and remove its container, token file and record from this host.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/cloudflare.connector.stop/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/cloudflare.connector.stop/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `tunnel` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `removed` | boolean | yes |  |
| `tunnel` | string | yes |  |

## cloudflare.expose.container v1

Attach a tunnel's connector to a container's network and route a hostname of the tunnel to the container.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/cloudflare.expose.container/v1/plan` |
| apply | `POST /api/v1/actions/cloudflare.expose.container/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `container` | string | yes |  |
| `hostId` | string | yes |  |
| `hostname` | string | yes |  |
| `network` | string | yes |  |
| `port` | integer | yes |  |
| `tunnel` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostname` | string | yes |  |
| `network` | string | yes |  |
| `service` | string | yes |  |

## cloudflare.expose.remove v1

Remove an exposed container's hostname and the network claims of its exposure.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/cloudflare.expose.remove/v1/plan` |
| apply | `POST /api/v1/actions/cloudflare.expose.remove/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes |  |
| `hostname` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostname` | string | yes |  |

## cloudflare.hostname.add v1

Route a public hostname to a service through a managed Cloudflare tunnel.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/cloudflare.hostname.add/v1/plan` |
| apply | `POST /api/v1/actions/cloudflare.hostname.add/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostname` | string | yes |  |
| `noTLSVerify` | boolean | no |  |
| `service` | string | yes |  |
| `tunnel` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `checkedAt` | string (date-time) or null | no |  |
| `health` | string | yes |  |
| `hostname` | string | yes |  |
| `phase` | string | yes |  |
| `recordId` | string | no |  |
| `service` | string | yes |  |
| `tunnel` | string | yes |  |

## cloudflare.hostname.list v1

List the public hostnames of the managed Cloudflare tunnels.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/cloudflare.hostname.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostnames` | array of object | yes |  |
| `hostnames[].checkedAt` | string (date-time) or null | no |  |
| `hostnames[].health` | string | yes |  |
| `hostnames[].hostname` | string | yes |  |
| `hostnames[].phase` | string | yes |  |
| `hostnames[].recordId` | string | no |  |
| `hostnames[].service` | string | yes |  |
| `hostnames[].tunnel` | string | yes |  |

## cloudflare.hostname.remove v1

Remove a public hostname's DNS record and ingress rule.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/cloudflare.hostname.remove/v1/plan` |
| apply | `POST /api/v1/actions/cloudflare.hostname.remove/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostname` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `checkedAt` | string (date-time) or null | no |  |
| `health` | string | yes |  |
| `hostname` | string | yes |  |
| `phase` | string | yes |  |
| `recordId` | string | no |  |
| `service` | string | yes |  |
| `tunnel` | string | yes |  |

## cloudflare.network.attach v1

Join the tunnel's connector to a Docker network and record the claimant that needs it.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/cloudflare.network.attach/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/cloudflare.network.attach/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `address` | string (ip-address) | no |  |
| `claimant` | string | yes |  |
| `network` | string | yes |  |
| `target` | string | no |  |
| `tunnel` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `attached` | boolean | yes |  |
| `claimant` | string | yes |  |
| `network` | string | yes |  |
| `preexisting` | boolean | yes |  |
| `tunnel` | string | yes |  |

## cloudflare.network.detach v1

Release a claimant's network claims on the tunnel's connector.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/cloudflare.network.detach/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/cloudflare.network.detach/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `claimant` | string | yes |  |
| `network` | string | yes |  |
| `tunnel` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `attached` | boolean | yes |  |
| `claimant` | string | yes |  |
| `network` | string | yes |  |
| `preexisting` | boolean | yes |  |
| `tunnel` | string | yes |  |

## cloudflare.tunnel.adopt v1

Accept a Cloudflare tunnel's current configuration as expected.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/cloudflare.tunnel.adopt/v1/plan` |
| apply | `POST /api/v1/actions/cloudflare.tunnel.adopt/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `tunnel` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |
| `name` | string | yes |  |

## cloudflare.tunnel.create v1

Create a remotely managed Cloudflare tunnel.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/cloudflare.tunnel.create/v1/plan` |
| apply | `POST /api/v1/actions/cloudflare.tunnel.create/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |
| `name` | string | yes |  |

## cloudflare.tunnel.delete v1

Delete a managed Cloudflare tunnel.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/cloudflare.tunnel.delete/v1/plan` |
| apply | `POST /api/v1/actions/cloudflare.tunnel.delete/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `tunnel` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |
| `name` | string | yes |  |

## cloudflare.tunnel.list v1

List the managed Cloudflare tunnels.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/cloudflare.tunnel.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `tunnels` | array of object | yes |  |
| `tunnels[].connections` | array of object or null | yes |  |
| `tunnels[].createdAt` | string (date-time) | yes |  |
| `tunnels[].drift` | boolean | yes |  |
| `tunnels[].hostnames` | integer | yes |  |
| `tunnels[].id` | string | yes |  |
| `tunnels[].name` | string | yes |  |
| `tunnels[].status` | string | yes |  |

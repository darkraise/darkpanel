# tailnet API

Back to the [API reference](README.md).

## tailnet.account.connect v1

Connect the tailnet with an OAuth client.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/tailnet.account.connect/v1/plan` |
| apply | `POST /api/v1/actions/tailnet.account.connect/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `clientId` | string | yes |  |
| `clientSecret` | string | yes | Secret. |
| `tagsAllowed` | array of string | yes |  |
| `tailnet` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `capabilities` | array of object | yes |  |
| `capabilities[].detail` | string | no |  |
| `capabilities[].name` | string | yes |  |
| `capabilities[].state` | string | yes |  |
| `clientId` | string | yes |  |
| `connectedAt` | string (date-time) | yes |  |
| `tagsAllowed` | array of string | yes |  |
| `tailnet` | string | yes |  |

## tailnet.account.disconnect v1

Disconnect the tailnet and delete the stored credentials.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/tailnet.account.disconnect/v1/plan` |
| apply | `POST /api/v1/actions/tailnet.account.disconnect/v1/apply` |

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

## tailnet.account.get v1

Show the connected tailnet account.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/tailnet.account.get/v1/read` |

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

## tailnet.authkey.create v1

Create a tagged auth key for devices to join the tailnet.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/tailnet.authkey.create/v1/plan` |
| apply | `POST /api/v1/actions/tailnet.authkey.create/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `ephemeral` | boolean | no |  |
| `expirySeconds` | integer | no |  |
| `preauthorized` | boolean | no |  |
| `reusable` | boolean | no |  |
| `tags` | array of string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `description` | string | yes |  |
| `expires` | string (date-time) | yes |  |
| `id` | string | yes |  |
| `key` | string | yes | Secret. |

## tailnet.authkey.list v1

List the auth keys Darkpanel requested.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/tailnet.authkey.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `keys` | array of object | yes |  |
| `keys[].description` | string | yes |  |
| `keys[].expires` | string (date-time) | yes |  |
| `keys[].keyId` | string | no |  |
| `keys[].kind` | string | yes |  |
| `keys[].ownerId` | string | yes |  |
| `keys[].requestedAt` | string (date-time) | yes |  |
| `keys[].state` | string | yes |  |

## tailnet.device.authorize v1

Authorize a device to join the tailnet.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/tailnet.device.authorize/v1/plan` |
| apply | `POST /api/v1/actions/tailnet.device.authorize/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `device` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `addresses` | array of string | yes |  |
| `advertisedRoutes` | array of string | yes |  |
| `authorized` | boolean | yes |  |
| `enabledRoutes` | array of string | yes |  |
| `exitNode` | boolean | yes |  |
| `exitNodeEnabled` | boolean | yes |  |
| `expires` | string | no |  |
| `hostname` | string | yes |  |
| `id` | string | yes |  |
| `keyExpiryDisabled` | boolean | yes |  |
| `lastSeen` | string | no |  |
| `name` | string | yes |  |
| `os` | string | yes |  |
| `tags` | array of string | yes |  |
| `user` | string | yes |  |

## tailnet.device.delete v1

Remove a device from the tailnet.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/tailnet.device.delete/v1/plan` |
| apply | `POST /api/v1/actions/tailnet.device.delete/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `device` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |
| `name` | string | yes |  |

## tailnet.device.expire v1

Expire a device's node key so it must authenticate again.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/tailnet.device.expire/v1/plan` |
| apply | `POST /api/v1/actions/tailnet.device.expire/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `device` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |
| `name` | string | yes |  |

## tailnet.device.list v1

List the tailnet's devices.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/tailnet.device.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `devices` | array of object | yes |  |
| `devices[].addresses` | array of string | yes |  |
| `devices[].advertisedRoutes` | array of string | yes |  |
| `devices[].authorized` | boolean | yes |  |
| `devices[].enabledRoutes` | array of string | yes |  |
| `devices[].exitNode` | boolean | yes |  |
| `devices[].exitNodeEnabled` | boolean | yes |  |
| `devices[].expires` | string | no |  |
| `devices[].hostname` | string | yes |  |
| `devices[].id` | string | yes |  |
| `devices[].keyExpiryDisabled` | boolean | yes |  |
| `devices[].lastSeen` | string | no |  |
| `devices[].name` | string | yes |  |
| `devices[].os` | string | yes |  |
| `devices[].tags` | array of string | yes |  |
| `devices[].user` | string | yes |  |

## tailnet.device.set-routes v1

Set the routes a device is approved to serve.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/tailnet.device.set-routes/v1/plan` |
| apply | `POST /api/v1/actions/tailnet.device.set-routes/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `device` | string | yes |  |
| `routes` | array of string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `advertisedRoutes` | array of string | yes |  |
| `enabledRoutes` | array of string | yes |  |

## tailnet.node.join v1

Join an enrolled host to the tailnet.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/tailnet.node.join/v1/plan` |
| apply | `POST /api/v1/actions/tailnet.node.join/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes |  |
| `install` | boolean | no |  |
| `tags` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `addresses` | array of string | yes |  |
| `backendState` | string | yes |  |

## tailnet.policy.get v1

Read the tailnet policy file with its comments.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/tailnet.policy.get/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `etag` | string | yes |  |
| `text` | string | yes |  |

## tailnet.policy.patch v1

Edit the tailnet policy file with typed, convergent edits.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/tailnet.policy.patch/v1/plan` |
| apply | `POST /api/v1/actions/tailnet.policy.patch/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `edits` | array of object | yes |  |
| `edits[].op` | string | yes |  |
| `edits[].path` | string | yes |  |
| `edits[].value` | any | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `etag` | string | yes |  |

## tailnet.policy.set v1

Replace the tailnet policy file.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/tailnet.policy.set/v1/plan` |
| apply | `POST /api/v1/actions/tailnet.policy.set/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `text` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `etag` | string | yes |  |

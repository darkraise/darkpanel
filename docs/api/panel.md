# panel API

Back to the [API reference](README.md).

## panel.exposure.add v1

Expose the panel on the controller's host through Nginx Proxy Manager or a Cloudflare tunnel.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/panel.exposure.add/v1/plan` |
| apply | `POST /api/v1/actions/panel.exposure.add/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes |  |
| `hostname` | string | yes |  |
| `port` | integer | no |  |
| `tunnel` | string | no |  |
| `via` | string | yes | Values: npm, tunnel. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `exposureId` | string | yes |  |
| `gateway` | string (ip-address) | yes |  |
| `hostId` | string | yes |  |
| `hostname` | string | yes |  |
| `port` | integer | yes |  |
| `proxy` | string (ip-address) | yes |  |
| `subnet` | string (ip-prefix) | yes |  |
| `tunnel` | string | no |  |
| `via` | string | yes |  |

## panel.exposure.remove v1

Revoke the panel's listener, then remove its proxy host or hostname, attachment, network and firewall rule.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/panel.exposure.remove/v1/plan` |
| apply | `POST /api/v1/actions/panel.exposure.remove/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `exposureId` | string | yes |  |
| `hostname` | string | yes |  |

## panel.exposure.status v1

Show whether the panel is exposed and how.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/panel.exposure.status/v1/read` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `exposed` | boolean | yes |  |
| `exposure` | object or null | no |  |

## panel.listener.open v1

Open the runtime listener that serves the exposed panel.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/panel.listener.open/v1/plan` |
| apply | `POST /api/v1/actions/panel.listener.open/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `gateway` | string (ip-address) | yes |  |
| `hostId` | string | yes |  |
| `hostname` | string | yes |  |
| `port` | integer | yes |  |
| `proxy` | string (ip-address) | yes |  |
| `subnet` | string (ip-prefix) | yes |  |
| `tunnel` | string | no |  |
| `via` | string | yes | Values: npm, tunnel. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `address` | string (ip-address-port) | yes |  |
| `exposureId` | string | yes |  |
| `listenerId` | string | yes |  |
| `ruleId` | string | yes |  |

## panel.network.create v1

Create the panel network between the proxy and the panel's listener on a free /29 of 172.31.255.0/24.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/panel.network.create/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/panel.network.create/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `gateway` | string (ip-address) | yes |  |
| `id` | string | yes |  |
| `network` | string | yes |  |
| `proxy` | string (ip-address) | yes |  |
| `subnet` | string (ip-prefix) | yes |  |

## panel.network.remove v1

Remove the panel network between the proxy and the panel's listener.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/panel.network.remove/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/panel.network.remove/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `network` | string | yes |  |
| `removed` | boolean | yes |  |

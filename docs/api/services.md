# services API

Back to the [API reference](README.md).

## services.journal.query v1

Read a page of journal entries by unit, priority, time range and message text.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/services.journal.query/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `cursor` | string | no |  |
| `priority` | integer | no |  |
| `since` | integer | no |  |
| `substring` | string | no |  |
| `unit` | string | no |  |
| `until` | integer | no |  |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `cursor` | string | no |  |
| `entries` | array of object | yes |  |
| `entries[].message` | string | yes |  |
| `entries[].pid` | integer | no |  |
| `entries[].priority` | integer or null | yes |  |
| `entries[].time` | integer | yes |  |
| `entries[].truncated` | boolean | no |  |
| `entries[].unit` | string | no |  |
| `more` | boolean | yes |  |

## services.unit.disable v1

Disable a service, socket or timer at boot.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/services.unit.disable/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/services.unit.disable/v1/apply` |

Minimum scope: `admin`.

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
| `unit` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `leftInactive` | integer | no |  |
| `unit` | string | yes |  |

## services.unit.enable v1

Enable a service, socket or timer at boot.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/services.unit.enable/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/services.unit.enable/v1/apply` |

Minimum scope: `admin`.

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
| `unit` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `leftInactive` | integer | no |  |
| `unit` | string | yes |  |

## services.unit.get v1

Show one unit: its state, memory, tasks, restarts, start time, unit file and drop-ins.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/services.unit.get/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `unit` | string | yes |  |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `active` | string | yes |  |
| `description` | string | yes |  |
| `dropIns` | array of string | yes |  |
| `enabled` | string | yes |  |
| `load` | string | yes |  |
| `mainPid` | integer | no |  |
| `memoryCurrent` | integer or null | no |  |
| `name` | string | yes |  |
| `restarts` | integer | yes |  |
| `startedAt` | string (date-time) or null | no |  |
| `sub` | string | yes |  |
| `tasks` | integer or null | no |  |
| `truncated` | boolean | no | Whether the unit file text was cut at 64 KiB. |
| `type` | string | yes |  |
| `unitFile` | string | no |  |
| `unitFileText` | string | no | The unit file text, at most 64 KiB; empty unless the caller has the admin scope, since a unit can hold secrets. |

## services.unit.list v1

List the services, sockets, timers, mounts, paths and targets of the host with their state.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/services.unit.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `state` | string | no |  |
| `type` | string | no |  |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `units` | array of object | yes |  |
| `units[].active` | string | yes |  |
| `units[].description` | string | yes |  |
| `units[].enabled` | string | yes |  |
| `units[].load` | string | yes |  |
| `units[].mainPid` | integer | no |  |
| `units[].name` | string | yes |  |
| `units[].sub` | string | yes |  |
| `units[].type` | string | yes |  |

## services.unit.reload v1

Reload the configuration of a service, socket or timer.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/services.unit.reload/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/services.unit.reload/v1/apply` |

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
| `unit` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `leftInactive` | integer | no |  |
| `unit` | string | yes |  |

## services.unit.restart v1

Restart a service, socket or timer.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/services.unit.restart/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/services.unit.restart/v1/apply` |

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
| `unit` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `leftInactive` | integer | no |  |
| `unit` | string | yes |  |

## services.unit.start v1

Start a service, socket or timer.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/services.unit.start/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/services.unit.start/v1/apply` |

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
| `unit` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `leftInactive` | integer | no |  |
| `unit` | string | yes |  |

## services.unit.stop v1

Stop a service, socket or timer.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/services.unit.stop/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/services.unit.stop/v1/apply` |

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
| `unit` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `leftInactive` | integer | no |  |
| `unit` | string | yes |  |

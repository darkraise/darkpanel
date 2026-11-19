# system API

Back to the [API reference](README.md).

## system.autoupdates

Read system.autoupdates

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/system/autoupdates` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `enabled` | boolean | yes |  |
| `installed` | boolean | yes |  |

## system.docker

Read system.docker

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/system/docker` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `conflicts` | array of string | no |  |
| `containers` | integer or null | no |  |
| `images` | integer or null | no |  |
| `labelled` | array of object | no |  |
| `labelled[].module` | string | yes |  |
| `labelled[].name` | string | yes |  |
| `running` | boolean or null | no |  |
| `state` | string | yes |  |
| `updateAvailable` | boolean or null | no |  |
| `version` | string | no |  |
| `volumes` | integer or null | no |  |

## system.packages

Read system.packages

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/system/packages` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

A list of:

| Field | Type | Required | Description |
|---|---|---|---|
| `arch` | string | yes |  |
| `description` | string | yes |  |
| `name` | string | yes |  |
| `protected` | string or null | yes |  |
| `version` | string | yes |  |

## system.packages.search v1

Search the configured package sources for names containing a term.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/system.packages.search/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `term` | string | yes |  |

### Output

Returned in the `output` field of the response.

A list of:

| Field | Type | Required | Description |
|---|---|---|---|
| `candidate` | string | yes |  |
| `installed` | boolean | yes |  |
| `name` | string | yes |  |

## system.reboot

Read system.reboot

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/system/reboot` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `kernel` | object or null | yes |  |
| `packages` | array of string | yes |  |
| `required` | boolean | yes |  |
| `services` | array of string | yes |  |

## system.updates

Read system.updates

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/system/updates` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `dpkgInterrupted` | boolean | yes |  |
| `failedSources` | array of string | yes |  |
| `heldBack` | array of object | yes |  |
| `heldBack[].name` | string | yes |  |
| `heldBack[].reason` | string | yes |  |
| `heldBack[].version` | string | yes |  |
| `refreshedAt` | string or null | yes |  |
| `removals` | array of string | yes |  |
| `upgradable` | array of object | yes |  |
| `upgradable[].from` | string | yes |  |
| `upgradable[].name` | string | yes |  |
| `upgradable[].origin` | string | yes |  |
| `upgradable[].security` | boolean | yes |  |
| `upgradable[].to` | string | yes |  |

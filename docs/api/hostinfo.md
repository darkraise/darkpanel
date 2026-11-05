# hostinfo API

Back to the [API reference](README.md).

## hostinfo.summary

Read hostinfo.summary

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/hostinfo/summary` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `arch` | string | yes |  |
| `bootTime` | string (date-time) | yes |  |
| `cpu` | object | yes |  |
| `cpu.logicalCount` | integer | yes |  |
| `cpu.model` | string | yes |  |
| `darkpanelVersion` | string | yes |  |
| `hostname` | string | yes |  |
| `kernel` | string | yes |  |
| `load` | object | yes |  |
| `load.fifteen` | number | yes |  |
| `load.five` | number | yes |  |
| `load.one` | number | yes |  |
| `memory` | object | yes |  |
| `memory.availableBytes` | integer | yes |  |
| `memory.totalBytes` | integer | yes |  |
| `os` | object | yes |  |
| `os.id` | string | yes |  |
| `os.prettyName` | string | yes |  |
| `os.versionId` | string | yes |  |
| `rootFs` | object | yes |  |
| `rootFs.availableBytes` | integer | yes |  |
| `rootFs.totalBytes` | integer | yes |  |
| `swap` | object | yes |  |
| `swap.freeBytes` | integer | yes |  |
| `swap.totalBytes` | integer | yes |  |
| `uptimeSeconds` | integer | yes |  |

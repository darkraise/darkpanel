# metrics API

Back to the [API reference](README.md).

## metrics.history.get v1

Return a host's stored metrics over a range, with its last poll.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/metrics.history.get/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes | Id of the host whose metrics to read. |
| `range` | string | no | How far back to read; 1h when left out. Up to 7d the points are minutes, beyond that hours. Values: 1h, 24h, 7d, 30d, 90d. |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes |  |
| `points` | array of object | yes | Oldest first. |
| `points[].cpu` | number | yes | CPU busy percent across all cores. |
| `points[].fs` | array of object | yes |  |
| `points[].fs[].mount` | string | yes |  |
| `points[].fs[].total` | integer | yes |  |
| `points[].fs[].used` | integer | yes |  |
| `points[].load1` | number | yes |  |
| `points[].load15` | number | yes |  |
| `points[].load5` | number | yes |  |
| `points[].memTotal` | integer | yes |  |
| `points[].memUsed` | integer | yes |  |
| `points[].net` | array of object | yes |  |
| `points[].net[].iface` | string | yes |  |
| `points[].net[].rxBps` | number | yes |  |
| `points[].net[].txBps` | number | yes |  |
| `points[].swapTotal` | integer | yes |  |
| `points[].swapUsed` | integer | yes |  |
| `points[].ts` | string (date-time) | yes | The start of the minute or the hour. |
| `range` | string | yes |  |
| `resolution` | string | yes | 1m for minute points, 1h for hour points. Values: 1m, 1h. |
| `status` | object or null | yes |  |

## metrics.samples.get v1

Return the 1-minute averages of the host's recent metrics.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/metrics.samples.get/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `since` | string | no | RFC 3339; only minutes that start after it. Empty for every minute held. |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `now` | string (date-time) | yes | The agent's clock. |
| `samples` | array of object | yes | The averages of complete minutes after since, oldest first, at most 60. |
| `samples[].cpu` | number | yes | CPU busy percent across all cores. |
| `samples[].fs` | array of object | yes | Local filesystems, the first 16 by mount point. |
| `samples[].fs[].mount` | string | yes |  |
| `samples[].fs[].total` | integer | yes |  |
| `samples[].fs[].used` | integer | yes |  |
| `samples[].load1` | number | yes |  |
| `samples[].load15` | number | yes |  |
| `samples[].load5` | number | yes |  |
| `samples[].memTotal` | integer | yes |  |
| `samples[].memUsed` | integer | yes | Memory in use in bytes: the total less the available memory. |
| `samples[].net` | array of object | yes | Interfaces other than lo, the first 16 by name. |
| `samples[].net[].iface` | string | yes |  |
| `samples[].net[].rxBps` | number | yes |  |
| `samples[].net[].txBps` | number | yes |  |
| `samples[].swapTotal` | integer | yes |  |
| `samples[].swapUsed` | integer | yes |  |
| `samples[].ts` | string (date-time) | yes | When the sample was taken; for a minute average, the start of the minute. |

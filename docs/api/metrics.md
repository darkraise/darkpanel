# metrics API

Back to the [API reference](README.md).

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

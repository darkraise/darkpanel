# metrics API

Back to the [API reference](README.md).

## metrics.alert.create v1

Add an alert rule: a metric, a threshold held for a number of minutes, a severity and the hosts it covers.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/metrics.alert.create/v1/plan` |
| apply | `POST /api/v1/actions/metrics.alert.create/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `comparator` | string | no | \> or \<; required except for offline, which ignores it. Values: \>, \<. |
| `durationMin` | integer | yes | How many minutes the condition must hold before the rule fires. |
| `metric` | string | yes | cpu, memory, swap and disk compare a percent (disk per mounted filesystem), load the 1-minute load per logical CPU; offline fires on a host with no live session. Values: cpu, memory, swap, disk, load, offline. |
| `name` | string | yes | The name: 1 to 64 letters, digits, spaces, dots, underscores or hyphens, starting with a letter or digit; unique without regard to case. |
| `severity` | string | yes | Values: warning, critical. |
| `target` | string | yes | * for every host, or the id of a host group. |
| `threshold` | number | no | 0 to 100 for a percent, 0 to 1000 for load; offline ignores it. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `comparator` | string | yes | \> or \<; \> for offline, which ignores it. |
| `createdAt` | string (date-time) | yes |  |
| `durationMin` | integer | yes |  |
| `enabled` | boolean | yes |  |
| `id` | string | yes |  |
| `metric` | string | yes |  |
| `name` | string | yes |  |
| `severity` | string | yes |  |
| `target` | string | yes | * for every host, or a host group's id. |
| `targetName` | string | yes | The target group's name; empty for every host. |
| `threshold` | number | yes |  |
| `updatedAt` | string (date-time) | yes |  |

## metrics.alert.delete v1

Delete an alert rule with its alerts; the confirmation phrase is its name.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/metrics.alert.delete/v1/plan` |
| apply | `POST /api/v1/actions/metrics.alert.delete/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `comparator` | string | yes | \> or \<; \> for offline, which ignores it. |
| `createdAt` | string (date-time) | yes |  |
| `durationMin` | integer | yes |  |
| `enabled` | boolean | yes |  |
| `id` | string | yes |  |
| `metric` | string | yes |  |
| `name` | string | yes |  |
| `severity` | string | yes |  |
| `target` | string | yes | * for every host, or a host group's id. |
| `targetName` | string | yes | The target group's name; empty for every host. |
| `threshold` | number | yes |  |
| `updatedAt` | string (date-time) | yes |  |

## metrics.alert.list-rules v1

List the alert rules.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/metrics.alert.list-rules/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `rules` | array of object | yes | Every alert rule, sorted by name. |
| `rules[].comparator` | string | yes | \> or \<; \> for offline, which ignores it. |
| `rules[].createdAt` | string (date-time) | yes |  |
| `rules[].durationMin` | integer | yes |  |
| `rules[].enabled` | boolean | yes |  |
| `rules[].id` | string | yes |  |
| `rules[].metric` | string | yes |  |
| `rules[].name` | string | yes |  |
| `rules[].severity` | string | yes |  |
| `rules[].target` | string | yes | * for every host, or a host group's id. |
| `rules[].targetName` | string | yes | The target group's name; empty for every host. |
| `rules[].threshold` | number | yes |  |
| `rules[].updatedAt` | string (date-time) | yes |  |

## metrics.alert.list v1

List the firing alerts and those resolved in the last 24 hours.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/metrics.alert.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `alerts` | array of object | yes | Firing alerts, then those resolved in the last 24 hours, newest first; only hosts the caller may read. |
| `alerts[].hostId` | string | yes |  |
| `alerts[].hostName` | string | yes |  |
| `alerts[].metric` | string | yes |  |
| `alerts[].ruleId` | string | yes |  |
| `alerts[].ruleName` | string | yes |  |
| `alerts[].severity` | string | yes |  |
| `alerts[].since` | string (date-time) | yes | When the alert fired, or when it resolved. |
| `alerts[].state` | string | yes | Values: firing, resolved. |
| `alerts[].subject` | string | yes | The mount point of a disk alert; empty otherwise. |
| `alerts[].value` | number | yes | The figure at that moment: a percent, load per logical CPU, or minutes offline. |

## metrics.alert.update v1

Change an alert rule; its alerts are cleared without notice and evaluated again.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/metrics.alert.update/v1/plan` |
| apply | `POST /api/v1/actions/metrics.alert.update/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `comparator` | string | no | \> or \<; required except for offline, which ignores it. Values: \>, \<. |
| `durationMin` | integer | yes | How many minutes the condition must hold before the rule fires. |
| `enabled` | boolean | yes | Whether the rule is evaluated. |
| `id` | string | yes |  |
| `metric` | string | yes | cpu, memory, swap and disk compare a percent (disk per mounted filesystem), load the 1-minute load per logical CPU; offline fires on a host with no live session. Values: cpu, memory, swap, disk, load, offline. |
| `name` | string | yes | The name: 1 to 64 letters, digits, spaces, dots, underscores or hyphens, starting with a letter or digit; unique without regard to case. |
| `severity` | string | yes | Values: warning, critical. |
| `target` | string | yes | * for every host, or the id of a host group. |
| `threshold` | number | no | 0 to 100 for a percent, 0 to 1000 for load; offline ignores it. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `comparator` | string | yes | \> or \<; \> for offline, which ignores it. |
| `createdAt` | string (date-time) | yes |  |
| `durationMin` | integer | yes |  |
| `enabled` | boolean | yes |  |
| `id` | string | yes |  |
| `metric` | string | yes |  |
| `name` | string | yes |  |
| `severity` | string | yes |  |
| `target` | string | yes | * for every host, or a host group's id. |
| `targetName` | string | yes | The target group's name; empty for every host. |
| `threshold` | number | yes |  |
| `updatedAt` | string (date-time) | yes |  |

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
| `cpus` | integer | yes | The host's online logical CPUs at the last sample; 0 before the first. |
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

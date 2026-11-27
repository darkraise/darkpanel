# jobs API

Back to the [API reference](README.md).

## jobs.job.create v1

Create a job: a root-only script, a service that systemd hands it to and a timer that starts it.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/jobs.job.create/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/jobs.job.create/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `catchUp` | boolean | no | Run once at boot when a scheduled run was missed. |
| `command` | string | yes | The shell commands the job runs, at most 16 KiB. Secret. |
| `description` | string | no | A one-line description. |
| `randomDelaySeconds` | integer | no | Delay each run by up to this many seconds, at most 1 hour. |
| `schedule` | string | yes | A systemd calendar expression, such as daily or *-*-* 03:00:00. |
| `slug` | string | yes | The job's name: lower case letters, digits and hyphens. |
| `timeoutSeconds` | integer | no | How long a run may take, 1 minute to 24 hours; 1 hour when omitted. |
| `user` | string | yes | The account the job runs as: root or a login user. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `note` | string | no | Set when jobs is off on this host: the job's timer starts when jobs is turned on. |
| `slug` | string | yes |  |

## jobs.job.delete v1

Delete a job: stop its timer and remove its files.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/jobs.job.delete/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/jobs.job.delete/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `slug` | string | yes | The job's name. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `note` | string | no | Set when jobs is off on this host: the job's timer starts when jobs is turned on. |
| `slug` | string | yes |  |

## jobs.job.disable v1

Stop a job's timer and keep it off at boot.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/jobs.job.disable/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/jobs.job.disable/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `slug` | string | yes | The job's name. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `note` | string | no | Set when jobs is off on this host: the job's timer starts when jobs is turned on. |
| `slug` | string | yes |  |

## jobs.job.enable v1

Start a job's timer and start it at boot.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/jobs.job.enable/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/jobs.job.enable/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `slug` | string | yes | The job's name. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `note` | string | no | Set when jobs is off on this host: the job's timer starts when jobs is turned on. |
| `slug` | string | yes |  |

## jobs.job.list v1

List Darkpanel jobs, the host's other timers and its cron entries.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/jobs.job.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `cron` | array of object | yes |  |
| `cron[].command` | string | no |  |
| `cron[].schedule` | string | yes |  |
| `cron[].source` | string | yes |  |
| `cron[].user` | string | yes |  |
| `jobs` | array of object | yes |  |
| `jobs[].catchUp` | boolean | yes |  |
| `jobs[].command` | string | no |  |
| `jobs[].description` | string | no |  |
| `jobs[].enabled` | boolean | yes |  |
| `jobs[].exitStatus` | integer or null | no |  |
| `jobs[].lastResult` | string | no |  |
| `jobs[].lastStart` | string | no |  |
| `jobs[].nextRun` | string | no |  |
| `jobs[].randomDelaySeconds` | integer | yes |  |
| `jobs[].running` | boolean | yes |  |
| `jobs[].schedule` | string | yes |  |
| `jobs[].slug` | string | yes |  |
| `jobs[].timeoutSeconds` | integer | yes |  |
| `jobs[].user` | string | yes |  |
| `timers` | array of object | yes |  |
| `timers[].lastRun` | string | no |  |
| `timers[].name` | string | yes |  |
| `timers[].nextRun` | string | no |  |
| `timers[].source` | string | yes |  |
| `timers[].unit` | string | yes |  |

## jobs.job.run v1

Run a job now.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/jobs.job.run/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/jobs.job.run/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `slug` | string | yes | The job's name. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `note` | string | no | Set when jobs is off on this host: the job's timer starts when jobs is turned on. |
| `slug` | string | yes |  |

## jobs.job.update v1

Change a job's schedule, user, command or limits.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/jobs.job.update/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/jobs.job.update/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `catchUp` | boolean | no | Run once at boot when a scheduled run was missed. |
| `command` | string | yes | The shell commands the job runs, at most 16 KiB. Secret. |
| `description` | string | no | A one-line description. |
| `randomDelaySeconds` | integer | no | Delay each run by up to this many seconds, at most 1 hour. |
| `schedule` | string | yes | A systemd calendar expression, such as daily or *-*-* 03:00:00. |
| `slug` | string | yes | The job's name: lower case letters, digits and hyphens. |
| `timeoutSeconds` | integer | no | How long a run may take, 1 minute to 24 hours; 1 hour when omitted. |
| `user` | string | yes | The account the job runs as: root or a login user. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `note` | string | no | Set when jobs is off on this host: the job's timer starts when jobs is turned on. |
| `slug` | string | yes |  |

## jobs.schedule.check v1

Check a calendar expression and show its next three runs.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/jobs.schedule.check/v1/read` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `schedule` | string | yes | A systemd calendar expression. |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `next` | array of string | yes |  |

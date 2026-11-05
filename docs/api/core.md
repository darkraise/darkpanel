# core API

Back to the [API reference](README.md).

## core.audit.list v1

List audit log entries, newest first, filtered by actor, host or action.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/core.audit.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `action` | string | no | Only entries of this action, such as core.token.create. |
| `actor` | string | no | Only entries of this actor, such as user:\<id\> or local:root@\<host id\>. |
| `before` | string | no | The nextBefore of a previous page: only older entries are returned. |
| `hostId` | string | no | Only entries of agent actions on this host. |
| `limit` | integer | no | Most entries to return, 1 to 500; 100 when omitted. |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `entries` | array of object | yes | Matching entries, newest first. |
| `entries[].action` | string | yes | The action, or core.auth.login and core.auth.logout. |
| `entries[].actor` | string | yes | Who acted, such as user:\<id\>, token:\<id\>, local:root@\<host id\>, anonymous or system:recovery. |
| `entries[].at` | string (date-time) | yes | When the event happened. |
| `entries[].changeId` | string or null | yes |  |
| `entries[].errorCode` | string or null | yes |  |
| `entries[].hostId` | string or null | yes |  |
| `entries[].id` | string | yes | Entry id. |
| `entries[].input` | any | yes | The input, with secret fields redacted when it was recorded. |
| `entries[].phase` | string | yes | apply, confirm, revert, recover, interrupt, login or logout. |
| `entries[].result` | string | yes | ok or error. |
| `entries[].source` | string or null | yes |  |
| `entries[].version` | integer | yes | The action version. |
| `nextBefore` | string or null | yes |  |

## core.host.create v1

Create a pending host and return its one-hour enrollment token, shown only once.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/core.host.create/v1/plan` |
| apply | `POST /api/v1/actions/core.host.create/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `controllerAddress` | string | no | host:port the agent dials; the controller's --agent-advertise address when omitted. |
| `name` | string | yes | Host name: lower-case letters, digits, dots and hyphens, starting with a letter or digit. |
| `tags` | array of string | no | Tailscale tags of the new node, each tag:\<name\>; only with tailnetJoin, and each within the tailnet account's tagsAllowed. |
| `tailnetJoin` | boolean | no | Join the new host to the tailnet before it enrolls: the answer carries a dpb2. bootstrap bundle instead of only the token. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `bundle` | string | no | Single-use dpb2. bootstrap bundle holding the token and a tailnet auth key; set only when tailnetJoin was asked; shown only once. Secret. |
| `expiresAt` | string (date-time) | yes | When the token stops working, one hour after it was made. |
| `host` | object | yes | The host the token enrolls. |
| `host.agentVersion` | string or null | yes |  |
| `host.arch` | string or null | yes |  |
| `host.id` | string | yes | Host id. |
| `host.ingestion` | object or null | yes |  |
| `host.isLocal` | boolean | yes | Whether the host runs this controller. |
| `host.lastSeenAt` | string (date-time) or null | yes |  |
| `host.modules` | array of object | yes | Every compiled-in module's state on the host. |
| `host.modules[].available` | boolean | yes | Whether the module's migrations and preflight checks passed on the host. |
| `host.modules[].checkedAt` | string (date-time) or null | yes |  |
| `host.modules[].enabled` | boolean | yes | Whether an operator enabled the module on the host. |
| `host.modules[].id` | string | yes | Module id. |
| `host.modules[].reason` | string or null | yes |  |
| `host.modules[].version` | integer | yes | Module version compiled into this binary. |
| `host.name` | string | yes | Host name. |
| `host.online` | boolean | yes | Whether the host's agent has a live session with the controller. |
| `host.os` | string or null | yes |  |
| `host.protocolVersion` | integer or null | yes |  |
| `host.state` | string | yes | Enrollment state: pending until the agent enrolls, revoked after core.host.revoke. Values: pending, enrolled, revoked. |
| `host.supported` | boolean | yes | Whether the agent's version is inside the controller's support window; an unsupported agent accepts only update actions. |
| `token` | string | yes | Single-use enrollment token for darkpanel agent enroll; shown only once. Secret. |

## core.host.delete v1

Delete a pending or revoked host other than the local one; the confirmation phrase is the host name.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/core.host.delete/v1/plan` |
| apply | `POST /api/v1/actions/core.host.delete/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes | Host id. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `agentVersion` | string or null | yes |  |
| `arch` | string or null | yes |  |
| `id` | string | yes | Host id. |
| `ingestion` | object or null | yes |  |
| `isLocal` | boolean | yes | Whether the host runs this controller. |
| `lastSeenAt` | string (date-time) or null | yes |  |
| `modules` | array of object | yes | Every compiled-in module's state on the host. |
| `modules[].available` | boolean | yes | Whether the module's migrations and preflight checks passed on the host. |
| `modules[].checkedAt` | string (date-time) or null | yes |  |
| `modules[].enabled` | boolean | yes | Whether an operator enabled the module on the host. |
| `modules[].id` | string | yes | Module id. |
| `modules[].reason` | string or null | yes |  |
| `modules[].version` | integer | yes | Module version compiled into this binary. |
| `name` | string | yes | Host name. |
| `online` | boolean | yes | Whether the host's agent has a live session with the controller. |
| `os` | string or null | yes |  |
| `protocolVersion` | integer or null | yes |  |
| `state` | string | yes | Enrollment state: pending until the agent enrolls, revoked after core.host.revoke. Values: pending, enrolled, revoked. |
| `supported` | boolean | yes | Whether the agent's version is inside the controller's support window; an unsupported agent accepts only update actions. |

## core.host.list v1

List the managed hosts with the state of every module on each.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/core.host.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `hosts` | array of object | yes | Every host, the local host first, then by name. |
| `hosts[].agentVersion` | string or null | yes |  |
| `hosts[].arch` | string or null | yes |  |
| `hosts[].id` | string | yes | Host id. |
| `hosts[].ingestion` | object or null | yes |  |
| `hosts[].isLocal` | boolean | yes | Whether the host runs this controller. |
| `hosts[].lastSeenAt` | string (date-time) or null | yes |  |
| `hosts[].modules` | array of object | yes | Every compiled-in module's state on the host. |
| `hosts[].modules[].available` | boolean | yes | Whether the module's migrations and preflight checks passed on the host. |
| `hosts[].modules[].checkedAt` | string (date-time) or null | yes |  |
| `hosts[].modules[].enabled` | boolean | yes | Whether an operator enabled the module on the host. |
| `hosts[].modules[].id` | string | yes | Module id. |
| `hosts[].modules[].reason` | string or null | yes |  |
| `hosts[].modules[].version` | integer | yes | Module version compiled into this binary. |
| `hosts[].name` | string | yes | Host name. |
| `hosts[].online` | boolean | yes | Whether the host's agent has a live session with the controller. |
| `hosts[].os` | string or null | yes |  |
| `hosts[].protocolVersion` | integer or null | yes |  |
| `hosts[].state` | string | yes | Enrollment state: pending until the agent enrolls, revoked after core.host.revoke. Values: pending, enrolled, revoked. |
| `hosts[].supported` | boolean | yes | Whether the agent's version is inside the controller's support window; an unsupported agent accepts only update actions. |

## core.host.revoke v1

Revoke a host's agent certificates and enrollment token and close its live session; the confirmation phrase is the host name.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/core.host.revoke/v1/plan` |
| apply | `POST /api/v1/actions/core.host.revoke/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes | Host id. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `agentVersion` | string or null | yes |  |
| `arch` | string or null | yes |  |
| `id` | string | yes | Host id. |
| `ingestion` | object or null | yes |  |
| `isLocal` | boolean | yes | Whether the host runs this controller. |
| `lastSeenAt` | string (date-time) or null | yes |  |
| `modules` | array of object | yes | Every compiled-in module's state on the host. |
| `modules[].available` | boolean | yes | Whether the module's migrations and preflight checks passed on the host. |
| `modules[].checkedAt` | string (date-time) or null | yes |  |
| `modules[].enabled` | boolean | yes | Whether an operator enabled the module on the host. |
| `modules[].id` | string | yes | Module id. |
| `modules[].reason` | string or null | yes |  |
| `modules[].version` | integer | yes | Module version compiled into this binary. |
| `name` | string | yes | Host name. |
| `online` | boolean | yes | Whether the host's agent has a live session with the controller. |
| `os` | string or null | yes |  |
| `protocolVersion` | integer or null | yes |  |
| `state` | string | yes | Enrollment state: pending until the agent enrolls, revoked after core.host.revoke. Values: pending, enrolled, revoked. |
| `supported` | boolean | yes | Whether the agent's version is inside the controller's support window; an unsupported agent accepts only update actions. |

## core.host.token v1

Issue a pending or revoked host other than the local one a new one-hour enrollment token, replacing any earlier one; shown only once. The confirmation phrase is the host name.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/core.host.token/v1/plan` |
| apply | `POST /api/v1/actions/core.host.token/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `controllerAddress` | string | no | host:port the agent dials; the controller's --agent-advertise address when omitted. |
| `hostId` | string | yes | Id of the pending or revoked host to enroll. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `bundle` | string | no | Single-use dpb2. bootstrap bundle holding the token and a tailnet auth key; set only when tailnetJoin was asked; shown only once. Secret. |
| `expiresAt` | string (date-time) | yes | When the token stops working, one hour after it was made. |
| `host` | object | yes | The host the token enrolls. |
| `host.agentVersion` | string or null | yes |  |
| `host.arch` | string or null | yes |  |
| `host.id` | string | yes | Host id. |
| `host.ingestion` | object or null | yes |  |
| `host.isLocal` | boolean | yes | Whether the host runs this controller. |
| `host.lastSeenAt` | string (date-time) or null | yes |  |
| `host.modules` | array of object | yes | Every compiled-in module's state on the host. |
| `host.modules[].available` | boolean | yes | Whether the module's migrations and preflight checks passed on the host. |
| `host.modules[].checkedAt` | string (date-time) or null | yes |  |
| `host.modules[].enabled` | boolean | yes | Whether an operator enabled the module on the host. |
| `host.modules[].id` | string | yes | Module id. |
| `host.modules[].reason` | string or null | yes |  |
| `host.modules[].version` | integer | yes | Module version compiled into this binary. |
| `host.name` | string | yes | Host name. |
| `host.online` | boolean | yes | Whether the host's agent has a live session with the controller. |
| `host.os` | string or null | yes |  |
| `host.protocolVersion` | integer or null | yes |  |
| `host.state` | string | yes | Enrollment state: pending until the agent enrolls, revoked after core.host.revoke. Values: pending, enrolled, revoked. |
| `host.supported` | boolean | yes | Whether the agent's version is inside the controller's support window; an unsupported agent accepts only update actions. |
| `token` | string | yes | Single-use enrollment token for darkpanel agent enroll; shown only once. Secret. |

## core.host.update-policy v1

Set the release channel a host follows and its weekly two-hour update window in UTC, or none for on-demand updates only.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/core.host.update-policy/v1/plan` |
| apply | `POST /api/v1/actions/core.host.update-policy/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `channel` | string | yes | Release channel the host follows. Values: stable, beta. |
| `hostId` | string | yes | Host id. |
| `schedule` | object or null | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `channel` | string | yes | Release channel the host follows. Values: stable, beta. |
| `hostId` | string | yes | Host id. |
| `schedule` | object or null | yes |  |

## core.module.disable v1

Disable a module on a host; its actions and resources answer module.unavailable.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/core.module.disable/v1/plan` |
| apply | `POST /api/v1/actions/core.module.disable/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes | Id of the host. |
| `moduleId` | string | yes | Id of a compiled-in module other than core. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `available` | boolean | yes | Whether the module's migrations and preflight checks passed on the host. |
| `checkedAt` | string (date-time) or null | yes |  |
| `enabled` | boolean | yes | Whether an operator enabled the module on the host. |
| `id` | string | yes | Module id. |
| `reason` | string or null | yes |  |
| `version` | integer | yes | Module version compiled into this binary. |

## core.module.enable v1

Enable a module on a host; its preflight checks run there again and set its availability.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/core.module.enable/v1/plan` |
| apply | `POST /api/v1/actions/core.module.enable/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes | Id of the host. |
| `moduleId` | string | yes | Id of a compiled-in module other than core. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `available` | boolean | yes | Whether the module's migrations and preflight checks passed on the host. |
| `checkedAt` | string (date-time) or null | yes |  |
| `enabled` | boolean | yes | Whether an operator enabled the module on the host. |
| `id` | string | yes | Module id. |
| `reason` | string or null | yes |  |
| `version` | integer | yes | Module version compiled into this binary. |

## core.module.list v1

List the state of every compiled-in module on one host.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/core.module.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes | Id of the host whose modules to list. |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `modules` | array of object | yes | Every compiled-in module's state on the host, in registration order. |
| `modules[].available` | boolean | yes | Whether the module's migrations and preflight checks passed on the host. |
| `modules[].checkedAt` | string (date-time) or null | yes |  |
| `modules[].enabled` | boolean | yes | Whether an operator enabled the module on the host. |
| `modules[].id` | string | yes | Module id. |
| `modules[].reason` | string or null | yes |  |
| `modules[].version` | integer | yes | Module version compiled into this binary. |

## core.token.create v1

Create an API token for the calling user; the token is shown only once.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/core.token.create/v1/plan` |
| apply | `POST /api/v1/actions/core.token.create/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `expiresInDays` | integer | no | Days until the token expires, 1 to 365; 90 when omitted. |
| `name` | string | yes | A name to recognise the token by, 1 to 64 characters. |
| `scope` | string | yes | What the token may do: read, operate or admin. Values: read, operate, admin. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `createdAt` | string (date-time) | yes |  |
| `expiresAt` | string (date-time) | yes |  |
| `id` | string | yes |  |
| `lastUsedAt` | string (date-time) or null | yes |  |
| `name` | string | yes |  |
| `revokedAt` | string (date-time) or null | yes |  |
| `scope` | string | yes |  |
| `token` | string | yes | The token, sent as Authorization: Bearer \<token\>; shown only once. Secret. |

## core.token.list v1

List the calling user's API tokens.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/core.token.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `tokens` | array of object | yes | The calling user's tokens, newest first; revoked and expired tokens stay listed until they are purged. |
| `tokens[].createdAt` | string (date-time) | yes |  |
| `tokens[].expiresAt` | string (date-time) | yes |  |
| `tokens[].id` | string | yes |  |
| `tokens[].lastUsedAt` | string (date-time) or null | yes |  |
| `tokens[].name` | string | yes |  |
| `tokens[].revokedAt` | string (date-time) or null | yes |  |
| `tokens[].scope` | string | yes |  |

## core.token.revoke v1

Revoke one of the calling user's API tokens; the confirmation phrase is the token id.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/core.token.revoke/v1/plan` |
| apply | `POST /api/v1/actions/core.token.revoke/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes | Id of one of the calling user's tokens. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `createdAt` | string (date-time) | yes |  |
| `expiresAt` | string (date-time) | yes |  |
| `id` | string | yes |  |
| `lastUsedAt` | string (date-time) or null | yes |  |
| `name` | string | yes |  |
| `revokedAt` | string (date-time) or null | yes |  |
| `scope` | string | yes |  |

## core.update.apply v1

Update this host to a signed release; the change completes before the services restart, and core.update.status follows the update.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/core.update.apply/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/core.update.apply/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `channel` | string | yes | Channel the version must be signed for. Values: stable, beta. |
| `force` | boolean | yes | Allow a downgrade of at most one minor version. |
| `version` | string | yes | Semantic version to install, such as 1.3.0 or 1.3.0-beta.1. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `error` | object or null | yes |  |
| `fromVersion` | string | yes | Version running before this step. |
| `id` | string | yes | Transaction id. |
| `kind` | string | yes | update or rollback. Values: update, rollback. |
| `operationId` | string | yes | Id of the operation: the id of its first transaction. |
| `parentId` | string or null | yes |  |
| `startedAt` | string (date-time) | yes | When the step started. |
| `state` | string | yes | Transaction state. Values: pending, downloading, staged, swapped, rolling-back, committed, rolled-back, failed. |
| `targetVersion` | string | yes | Version the whole operation ends on. |
| `toVersion` | string | yes | Version this step installs. |
| `updatedAt` | string (date-time) | yes | When the step last changed state. |

## core.update.rollback v1

Roll this host back to the binary its last update replaced; the confirmation phrase is that binary's version.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/core.update.rollback/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/core.update.rollback/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `force` | boolean | yes | Roll back although an enrolled agent runs a newer version than the previous binary. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `error` | object or null | yes |  |
| `fromVersion` | string | yes | Version running before this step. |
| `id` | string | yes | Transaction id. |
| `kind` | string | yes | update or rollback. Values: update, rollback. |
| `operationId` | string | yes | Id of the operation: the id of its first transaction. |
| `parentId` | string or null | yes |  |
| `startedAt` | string (date-time) | yes | When the step started. |
| `state` | string | yes | Transaction state. Values: pending, downloading, staged, swapped, rolling-back, committed, rolled-back, failed. |
| `targetVersion` | string | yes | Version the whole operation ends on. |
| `toVersion` | string | yes | Version this step installs. |
| `updatedAt` | string (date-time) | yes | When the step last changed state. |

## core.update.rollout-cancel v1

Stop a rollout: no further host is updated; the confirmation phrase is the rollout id.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/core.update.rollout-cancel/v1/plan` |
| apply | `POST /api/v1/actions/core.update.rollout-cancel/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `force` | boolean | yes | Mark the rollout cancelled at once, without waiting for a host's update to finish. |
| `rolloutId` | string | yes | Rollout id. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `channel` | string | yes | Channel the version is signed for. Values: stable, beta. |
| `createdAt` | string (date-time) | yes | When the rollout was created. |
| `error` | object or null | yes |  |
| `hosts` | array of object | yes | The rollout's hosts in order. |
| `hosts[].error` | object or null | yes |  |
| `hosts[].hostId` | string | yes | Host id. |
| `hosts[].operationId` | string or null | yes |  |
| `hosts[].planId` | string or null | yes |  |
| `hosts[].position` | integer | yes | Order in which the host is updated, from 0. |
| `hosts[].state` | string | yes | Host state within the rollout. Values: waiting, updating, committed, failed, skipped. |
| `id` | string | yes | Rollout id. |
| `state` | string | yes | Rollout state; draining means dispatch stopped while a host's update is still running; quarantined means a controller restore stopped it until it is resolved. Values: running, draining, quarantined, succeeded, failed, cancelled. |
| `updatedAt` | string (date-time) | yes | When the rollout last changed. |
| `version` | string | yes | Version being rolled out. |

## core.update.rollout v1

Update the listed hosts to a release one host at a time, the controller's own host first; the rollout stops at the first failure.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/core.update.rollout/v1/plan` |
| apply | `POST /api/v1/actions/core.update.rollout/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `channel` | string | yes | Channel the version must be signed for. Values: stable, beta. |
| `hosts` | array of string | yes | Ids of the hosts to update, in order; the controller's own host always goes first. |
| `version` | string | yes | Semantic version to roll out. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `channel` | string | yes | Channel the version is signed for. Values: stable, beta. |
| `createdAt` | string (date-time) | yes | When the rollout was created. |
| `error` | object or null | yes |  |
| `hosts` | array of object | yes | The rollout's hosts in order. |
| `hosts[].error` | object or null | yes |  |
| `hosts[].hostId` | string | yes | Host id. |
| `hosts[].operationId` | string or null | yes |  |
| `hosts[].planId` | string or null | yes |  |
| `hosts[].position` | integer | yes | Order in which the host is updated, from 0. |
| `hosts[].state` | string | yes | Host state within the rollout. Values: waiting, updating, committed, failed, skipped. |
| `id` | string | yes | Rollout id. |
| `state` | string | yes | Rollout state; draining means dispatch stopped while a host's update is still running; quarantined means a controller restore stopped it until it is resolved. Values: running, draining, quarantined, succeeded, failed, cancelled. |
| `updatedAt` | string (date-time) | yes | When the rollout last changed. |
| `version` | string | yes | Version being rolled out. |

## core.update.rollouts v1

List rollouts with their hosts, newest first.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/core.update.rollouts/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `limit` | integer | no | Most rollouts to return, 1 to 100; 20 when omitted. |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `rollouts` | array of object | yes | Rollouts, newest first. |
| `rollouts[].channel` | string | yes | Channel the version is signed for. Values: stable, beta. |
| `rollouts[].createdAt` | string (date-time) | yes | When the rollout was created. |
| `rollouts[].error` | object or null | yes |  |
| `rollouts[].hosts` | array of object | yes | The rollout's hosts in order. |
| `rollouts[].hosts[].error` | object or null | yes |  |
| `rollouts[].hosts[].hostId` | string | yes | Host id. |
| `rollouts[].hosts[].operationId` | string or null | yes |  |
| `rollouts[].hosts[].planId` | string or null | yes |  |
| `rollouts[].hosts[].position` | integer | yes | Order in which the host is updated, from 0. |
| `rollouts[].hosts[].state` | string | yes | Host state within the rollout. Values: waiting, updating, committed, failed, skipped. |
| `rollouts[].id` | string | yes | Rollout id. |
| `rollouts[].state` | string | yes | Rollout state; draining means dispatch stopped while a host's update is still running; quarantined means a controller restore stopped it until it is resolved. Values: running, draining, quarantined, succeeded, failed, cancelled. |
| `rollouts[].updatedAt` | string (date-time) | yes | When the rollout last changed. |
| `rollouts[].version` | string | yes | Version being rolled out. |

## core.update.status v1

List this host's update and rollback transactions, newest first.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/core.update.status/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `changeId` | string | no | Only the records of the operation this change started. |
| `operationId` | string | no | Only the records of this update operation. |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `transactions` | array of object | yes | The matching records, newest first. |
| `transactions[].error` | object or null | yes |  |
| `transactions[].fromVersion` | string | yes | Version running before this step. |
| `transactions[].id` | string | yes | Transaction id. |
| `transactions[].kind` | string | yes | update or rollback. Values: update, rollback. |
| `transactions[].operationId` | string | yes | Id of the operation: the id of its first transaction. |
| `transactions[].parentId` | string or null | yes |  |
| `transactions[].startedAt` | string (date-time) | yes | When the step started. |
| `transactions[].state` | string | yes | Transaction state. Values: pending, downloading, staged, swapped, rolling-back, committed, rolled-back, failed. |
| `transactions[].targetVersion` | string | yes | Version the whole operation ends on. |
| `transactions[].toVersion` | string | yes | Version this step installs. |
| `transactions[].updatedAt` | string (date-time) | yes | When the step last changed state. |

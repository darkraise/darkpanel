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

## system.autoupdates.set v1

Turn unattended-upgrades' automatic updates on or off.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/system.autoupdates.set/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/system.autoupdates.set/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `enabled` | boolean | yes | Turn automatic security updates on or off. |

### Output

Returned in the `output` field of the change that apply returns.

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

## system.docker.engine-install v1

Install Docker Engine from Docker's repository; undo removes the packages and repository files it added.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/system.docker.engine-install/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/system.docker.engine-install/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `installed` | array of string | yes |  |
| `removed` | array of string | yes |  |
| `upgraded` | array of string | yes |  |

## system.docker.engine-update v1

Upgrade the Docker Engine packages to the versions Docker's repository offers.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/system.docker.engine-update/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/system.docker.engine-update/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `installed` | array of string | yes |  |
| `removed` | array of string | yes |  |
| `upgraded` | array of string | yes |  |

## system.host.reboot v1

Reboot the host 15 seconds after apply.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/system.host.reboot/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/system.host.reboot/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `bootTimeBefore` | string | yes | The host's boot time before the reboot, RFC 3339. |
| `scheduledAt` | string | yes | When the host reboots, RFC 3339. |

## system.index.refresh v1

Refresh the package index of every configured source.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/system.index.refresh/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/system.index.refresh/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `failedSources` | array of string | yes | The sources apt could not fetch or verify, as "\<uri\> \<suite\>". |

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

## system.packages.install v1

Install named packages; undo removes exactly the packages it installed.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/system.packages.install/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/system.packages.install/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `names` | array of string | yes | The packages to install, as the configured sources name them. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `installed` | array of string | yes |  |
| `removed` | array of string | yes |  |
| `upgraded` | array of string | yes |  |

## system.packages.remove v1

Remove named packages, optionally with their configuration files.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/system.packages.remove/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/system.packages.remove/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `names` | array of string | yes | The installed packages to remove. |
| `purge` | boolean | no | Also delete their configuration files. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `installed` | array of string | yes |  |
| `removed` | array of string | yes |  |
| `upgraded` | array of string | yes |  |

## system.packages.repair v1

Finish an interrupted package run and fix broken dependencies.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/system.packages.repair/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/system.packages.repair/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `installed` | array of string | yes |  |
| `removed` | array of string | yes |  |
| `upgraded` | array of string | yes |  |

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

## system.packages.upgrade v1

Upgrade every package, or the named installed packages.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/system.packages.upgrade/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/system.packages.upgrade/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `all` | boolean | no | Upgrade every package, as apt-get dist-upgrade does; it may install new dependencies and remove packages. |
| `names` | array of string | no | Upgrade only these installed packages; empty when all is set. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `installed` | array of string | yes |  |
| `removed` | array of string | yes |  |
| `upgraded` | array of string | yes |  |

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

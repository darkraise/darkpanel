# fail2ban API

Back to the [API reference](README.md).

## fail2ban.ban.add v1

Ban an address in a jail.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/fail2ban.ban.add/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/fail2ban.ban.add/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `address` | string | yes | An IPv4 or IPv6 address. |
| `jail` | string | yes | A running jail, such as sshd. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `address` | string | yes |  |
| `jail` | string | yes |  |

## fail2ban.ban.list v1

List the addresses fail2ban bans, with each ban's start and end.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/fail2ban.ban.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `jail` | string | no | The jail whose bans to list; every running jail when empty. |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `bans` | array of object | yes |  |
| `bans[].address` | string | yes |  |
| `bans[].end` | string (date-time) or null | no |  |
| `bans[].jail` | string | yes |  |
| `bans[].start` | string (date-time) | yes |  |

## fail2ban.ban.remove v1

Unban an address in a jail.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/fail2ban.ban.remove/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/fail2ban.ban.remove/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `address` | string | yes | An IPv4 or IPv6 address. |
| `jail` | string | yes | A running jail, such as sshd. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `address` | string | yes |  |
| `jail` | string | yes |  |

## fail2ban.package.install v1

Install fail2ban and python3-systemd with Darkpanel's settings written first.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/fail2ban.package.install/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/fail2ban.package.install/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `packages` | array of string | yes |  |

## fail2ban.package.remove v1

Remove fail2ban and python3-systemd; Darkpanel's settings file stays.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/fail2ban.package.remove/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/fail2ban.package.remove/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `packages` | array of string | yes |  |

## fail2ban.settings.apply v1

Write fail2ban's jail settings and reload it; undo puts the previous settings back.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/fail2ban.settings.apply/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/fail2ban.settings.apply/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `bantimeMinutes` | integer | yes | How long a ban lasts, 1 minute to 30 days. |
| `findtimeMinutes` | integer | yes | The window failures are counted in, 1 minute to 1 day. |
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
| `ignoreIp` | array of string | yes | Addresses and networks never banned. Keep every fixed entry fail2ban.status.get lists; the rest are this host's own entries. |
| `maxRetry` | integer | yes | Failures within the window that ban an address. |
| `overrides` | array of string | no |  |
| `recidive` | boolean | yes | Ban for a week, on every port, an address banned again and again. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `bantimeMinutes` | integer | yes | How long a ban lasts, 1 minute to 30 days. |
| `findtimeMinutes` | integer | yes | The window failures are counted in, 1 minute to 1 day. |
| `ignoreIp` | array of string | yes | Addresses and networks never banned. Keep every fixed entry fail2ban.status.get lists; the rest are this host's own entries. |
| `maxRetry` | integer | yes | Failures within the window that ban an address. |
| `recidive` | boolean | yes | Ban for a week, on every port, an address banned again and again. |

## fail2ban.status.get v1

Read fail2ban's version, service state, jails and settings, and whether CrowdSec is installed.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/fail2ban.status.get/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `crowdsec` | boolean | yes |  |
| `ignoreFixed` | array of string | yes |  |
| `installed` | boolean | yes |  |
| `jails` | array of object | yes |  |
| `jails[].banned` | integer | yes |  |
| `jails[].failed` | integer | yes |  |
| `jails[].failedTotal` | integer | yes |  |
| `jails[].name` | string | yes |  |
| `service` | string | yes |  |
| `settings` | object or null | no |  |
| `version` | string | no |  |

# notify API

Back to the [API reference](README.md).

## notify.channel.create v1

Add a notification channel: ntfy, Telegram, Discord, email or a webhook.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/notify.channel.create/v1/plan` |
| apply | `POST /api/v1/actions/notify.channel.create/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `discord` | object | no | The settings of a Discord channel. |
| `discord.webhookUrl` | string | no | The channel's https://discord.com/api/webhooks/... URL; write-only, required on create. Secret. |
| `email` | object | no | The settings of an email channel. |
| `email.from` | string | yes | The sender address. |
| `email.host` | string | yes | The SMTP server's host name or address. |
| `email.password` | string | no | The SMTP password, with a user name; write-only, and an update keeps a stored one it omits. Secret. |
| `email.port` | integer | yes | The SMTP server's port. |
| `email.recipients` | array of string | yes | 1 to 10 recipient addresses. |
| `email.security` | string | yes | starttls, tls (implicit TLS) or none; none only to a loopback host. Values: starttls, tls, none. |
| `email.username` | string | no | The SMTP user name; omitted when the server needs no sign-in. |
| `groupIds` | array of string | no | The host groups whose hosts' events the channel receives; empty receives every host's and host-less events. |
| `kind` | string | yes | The channel kind. Values: ntfy, telegram, discord, email, webhook. |
| `kinds` | array of string | no | The event kinds the channel receives; empty receives every kind. |
| `minSeverity` | string | yes | The lowest severity the channel receives. Values: info, warning, critical. |
| `name` | string | yes | The name: 1 to 48 letters, digits, spaces, dots, underscores or hyphens, starting with a letter or digit. |
| `ntfy` | object | no | The settings of an ntfy channel. |
| `ntfy.server` | string | no | The ntfy server, an http or https URL; https://ntfy.sh when omitted. |
| `ntfy.token` | string | no | An access token sent as a bearer token; optional and write-only. Secret. |
| `ntfy.topic` | string | no | The topic, 1 to 64 letters, digits, hyphens or underscores; write-only, required on create. Secret. |
| `telegram` | object | no | The settings of a Telegram channel. |
| `telegram.botToken` | string | no | The bot's token from BotFather; write-only, required on create. Secret. |
| `telegram.chatId` | string | yes | The chat id, a number, or @ and a public channel's name. |
| `webhook` | object | no | The settings of a webhook channel. |
| `webhook.secret` | string | no | An HMAC-SHA256 key; the body's signature is sent as X-Darkpanel-Signature: sha256=\<hex\>. Optional and write-only. Secret. |
| `webhook.url` | string | no | The http or https URL the JSON body is posted to; write-only, required on create. Secret. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `createdAt` | string (date-time) | yes |  |
| `discord` | object or null | no |  |
| `email` | object or null | no |  |
| `enabled` | boolean | yes | Whether the channel receives events. |
| `groupIds` | array of string | yes | The host groups the channel is limited to; empty is every host. |
| `id` | string | yes | The channel's id. |
| `kind` | string | yes | ntfy, telegram, discord, email or webhook. |
| `kinds` | array of string | yes | The event kinds the channel receives; empty receives every kind. |
| `lastDelivery` | object or null | yes |  |
| `minSeverity` | string | yes | The lowest severity the channel receives. |
| `name` | string | yes | The channel's name. |
| `ntfy` | object or null | no |  |
| `telegram` | object or null | no |  |
| `webhook` | object or null | no |  |

## notify.channel.delete v1

Delete a notification channel with its pending deliveries; the confirmation phrase is its name.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/notify.channel.delete/v1/plan` |
| apply | `POST /api/v1/actions/notify.channel.delete/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes | The channel's id. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `createdAt` | string (date-time) | yes |  |
| `discord` | object or null | no |  |
| `email` | object or null | no |  |
| `enabled` | boolean | yes | Whether the channel receives events. |
| `groupIds` | array of string | yes | The host groups the channel is limited to; empty is every host. |
| `id` | string | yes | The channel's id. |
| `kind` | string | yes | ntfy, telegram, discord, email or webhook. |
| `kinds` | array of string | yes | The event kinds the channel receives; empty receives every kind. |
| `lastDelivery` | object or null | yes |  |
| `minSeverity` | string | yes | The lowest severity the channel receives. |
| `name` | string | yes | The channel's name. |
| `ntfy` | object or null | no |  |
| `telegram` | object or null | no |  |
| `webhook` | object or null | no |  |

## notify.channel.list v1

List the notification channels with their routing and last delivery; write-only values are never returned.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/notify.channel.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `channels` | array of object | yes | Every channel, sorted by name. |
| `channels[].createdAt` | string (date-time) | yes |  |
| `channels[].discord` | object or null | no |  |
| `channels[].email` | object or null | no |  |
| `channels[].enabled` | boolean | yes | Whether the channel receives events. |
| `channels[].groupIds` | array of string | yes | The host groups the channel is limited to; empty is every host. |
| `channels[].id` | string | yes | The channel's id. |
| `channels[].kind` | string | yes | ntfy, telegram, discord, email or webhook. |
| `channels[].kinds` | array of string | yes | The event kinds the channel receives; empty receives every kind. |
| `channels[].lastDelivery` | object or null | yes |  |
| `channels[].minSeverity` | string | yes | The lowest severity the channel receives. |
| `channels[].name` | string | yes | The channel's name. |
| `channels[].ntfy` | object or null | no |  |
| `channels[].telegram` | object or null | no |  |
| `channels[].webhook` | object or null | no |  |

## notify.channel.update v1

Change a notification channel's settings and routing; omitted write-only values are kept.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/notify.channel.update/v1/plan` |
| apply | `POST /api/v1/actions/notify.channel.update/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `discord` | object | no | The settings of a Discord channel. |
| `discord.webhookUrl` | string | no | The channel's https://discord.com/api/webhooks/... URL; write-only, required on create. Secret. |
| `email` | object | no | The settings of an email channel. |
| `email.from` | string | yes | The sender address. |
| `email.host` | string | yes | The SMTP server's host name or address. |
| `email.password` | string | no | The SMTP password, with a user name; write-only, and an update keeps a stored one it omits. Secret. |
| `email.port` | integer | yes | The SMTP server's port. |
| `email.recipients` | array of string | yes | 1 to 10 recipient addresses. |
| `email.security` | string | yes | starttls, tls (implicit TLS) or none; none only to a loopback host. Values: starttls, tls, none. |
| `email.username` | string | no | The SMTP user name; omitted when the server needs no sign-in. |
| `enabled` | boolean | yes | Whether the channel receives events. |
| `groupIds` | array of string | no | The host groups whose hosts' events the channel receives; empty receives every host's and host-less events. |
| `id` | string | yes | The channel's id. |
| `kinds` | array of string | no | The event kinds the channel receives; empty receives every kind. |
| `minSeverity` | string | yes | The lowest severity the channel receives. Values: info, warning, critical. |
| `name` | string | yes | The name, as notify.channel.create takes it. |
| `ntfy` | object | no | The settings of an ntfy channel. |
| `ntfy.server` | string | no | The ntfy server, an http or https URL; https://ntfy.sh when omitted. |
| `ntfy.token` | string | no | An access token sent as a bearer token; optional and write-only. Secret. |
| `ntfy.topic` | string | no | The topic, 1 to 64 letters, digits, hyphens or underscores; write-only, required on create. Secret. |
| `telegram` | object | no | The settings of a Telegram channel. |
| `telegram.botToken` | string | no | The bot's token from BotFather; write-only, required on create. Secret. |
| `telegram.chatId` | string | yes | The chat id, a number, or @ and a public channel's name. |
| `webhook` | object | no | The settings of a webhook channel. |
| `webhook.secret` | string | no | An HMAC-SHA256 key; the body's signature is sent as X-Darkpanel-Signature: sha256=\<hex\>. Optional and write-only. Secret. |
| `webhook.url` | string | no | The http or https URL the JSON body is posted to; write-only, required on create. Secret. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `createdAt` | string (date-time) | yes |  |
| `discord` | object or null | no |  |
| `email` | object or null | no |  |
| `enabled` | boolean | yes | Whether the channel receives events. |
| `groupIds` | array of string | yes | The host groups the channel is limited to; empty is every host. |
| `id` | string | yes | The channel's id. |
| `kind` | string | yes | ntfy, telegram, discord, email or webhook. |
| `kinds` | array of string | yes | The event kinds the channel receives; empty receives every kind. |
| `lastDelivery` | object or null | yes |  |
| `minSeverity` | string | yes | The lowest severity the channel receives. |
| `name` | string | yes | The channel's name. |
| `ntfy` | object or null | no |  |
| `telegram` | object or null | no |  |
| `webhook` | object or null | no |  |

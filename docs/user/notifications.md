# Notifications

Darkpanel sends a message when an alert fires or resolves, a change fails or
is reverted automatically, a rollout fails on a host, or an update is rolled
back. You choose where those messages go by adding channels in Settings, then
Notifications.

## Channels

A channel is one destination:

- **ntfy**: a topic on `https://ntfy.sh` or your own server, with an optional
  access token.
- **Telegram**: a bot token and a chat id.
- **Discord**: a channel webhook URL.
- **Email**: an SMTP server, port, security (STARTTLS, TLS, or none to a
  loopback server only), an optional user name and password, a sender and 1 to
  10 recipients.
- **Webhook**: a URL that receives a JSON body, signed with an optional
  HMAC-SHA256 key in the `X-Darkpanel-Signature: sha256=<hex>` header.

Tokens, passwords, topics and URLs that grant access are write-only: they are
sealed when saved and never shown again. To change one, enter a new value.

Each channel also chooses what it receives: a lowest severity (info, warning
or critical), the event kinds (none ticked means every kind) and the host
groups (none means every host, and events about no host).

## Add a channel

1. Press Add channel and pick the kind.
2. Fill in its settings and its routing, review and apply.
3. Press Send test on its card. The card shows "Delivered" or "Failed:" with
   the provider's status code.

From the CLI, `darkpanel notify channel create` takes the kind's settings in
the `--input` JSON:

```json
{"kind": "ntfy", "name": "ops-phone", "minSeverity": "warning", "ntfy": {"topic": "ops-alerts"}}
```

`darkpanel notify channel test --id <id>` sends a test message and
`darkpanel notify channel list` lists the channels with their last delivery.

## Delivery

A message is stored in the same transaction as the fact it reports, so a
restart loses none. Darkpanel retries a failed delivery after 1, 5, 15 and 60
minutes, then every hour for 24 hours, and then marks it failed and tells the
other channels. Each delivery waits at most 15 seconds and follows no
redirect. The same alert or change is not sent twice within 10 minutes.

Messages carry the host name, the action, the change id, the state and the
error code, never an action's input or output. A webhook body is:

```json
{"id": "<event id>", "kind": "alert.firing", "severity": "warning", "hostId": "<host id>", "title": "…", "body": "…", "createdAt": "…"}
```

A webhook can receive the same event twice; use its `id` to drop the repeat.

Delivery pauses while a restored controller is in quarantine, and the restore
marks unsent messages failed, because the backup cannot tell which were sent.

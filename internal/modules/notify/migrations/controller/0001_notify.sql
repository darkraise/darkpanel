-- The notify module's controller tables (M10 spec section 4), read and
-- written only through internal/modules/notify. Times are fixed-width
-- RFC 3339 UTC text, so they compare as text; lists are JSON arrays.

-- A channel: config_json holds its kind's settings that are not write-only
-- (with the first 4 characters of an ntfy topic or a webhook URL), and
-- secret_ct its write-only values as one JSON object, sealed with associated
-- data "notify_channels.secret_ct.<id>". kinds_json and group_ids_json are
-- its routing; empty arrays receive everything.
CREATE TABLE notify_channels (
    id             TEXT PRIMARY KEY,
    kind           TEXT NOT NULL CHECK (kind IN ('ntfy', 'telegram', 'discord', 'email', 'webhook')),
    name           TEXT NOT NULL,
    config_json    TEXT NOT NULL,
    secret_ct      BLOB NOT NULL,
    min_severity   TEXT NOT NULL CHECK (min_severity IN ('info', 'warning', 'critical')),
    kinds_json     TEXT NOT NULL DEFAULT '[]',
    group_ids_json TEXT NOT NULL DEFAULT '[]',
    enabled        INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at     TEXT NOT NULL
) STRICT;
CREATE UNIQUE INDEX notify_channels_name ON notify_channels (name COLLATE NOCASE);

-- An event; host_id is '' for a host-less event. A change's events are
-- emitted once each (the partial unique index); other kinds keep repeat
-- keys, and dedupe stores those without delivering them.
CREATE TABLE notify_events (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,
    severity   TEXT NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    host_id    TEXT NOT NULL DEFAULT '',
    title      TEXT NOT NULL,
    body       TEXT NOT NULL,
    dedupe_key TEXT NOT NULL,
    created_at TEXT NOT NULL
) STRICT;
CREATE UNIQUE INDEX notify_events_change_key ON notify_events (dedupe_key)
    WHERE kind IN ('change.failed', 'change.auto_reverted');
CREATE INDEX notify_events_dedupe ON notify_events (dedupe_key, created_at);
CREATE INDEX notify_events_created ON notify_events (created_at);

-- One delivery of an event to a channel. last_error is a short code (an
-- HTTP status, an SMTP reply code, timeout, restored), never a provider's
-- body; updated_at is when state last changed.
CREATE TABLE notify_deliveries (
    event_id   TEXT NOT NULL REFERENCES notify_events (id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL REFERENCES notify_channels (id) ON DELETE CASCADE,
    state      TEXT NOT NULL CHECK (state IN ('pending', 'sent', 'failed')),
    attempts   INTEGER NOT NULL DEFAULT 0,
    next_at    TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (event_id, channel_id)
) STRICT;
CREATE INDEX notify_deliveries_due ON notify_deliveries (state, next_at);
CREATE INDEX notify_deliveries_channel ON notify_deliveries (channel_id, state, updated_at);

-- The notifier's high-water mark over core_changes.updated_at. The cursor
-- reads 5 s before its mark again, for rows stamped before they committed;
-- nothing is mid-commit while migrations run, so the mark starts 5 s after
-- the newest existing change, or at '' when there is none, and history is
-- not replayed.
CREATE TABLE notify_cursor (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    updated_at TEXT NOT NULL
) STRICT;
INSERT INTO notify_cursor (id, updated_at)
    SELECT 1, coalesce(strftime('%Y-%m-%dT%H:%M:%S', max(updated_at), '+5 seconds') || substr(max(updated_at), 20), '')
    FROM core_changes;

-- restored_tx is the restore transaction whose pending deliveries the
-- sender has already marked failed.
CREATE TABLE notify_state (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    restored_tx TEXT
) STRICT;
INSERT INTO notify_state (id, restored_tx) VALUES (1, NULL);

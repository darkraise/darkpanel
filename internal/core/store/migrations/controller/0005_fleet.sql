-- Fleet tables (M2 spec sections 2, 3 and 8): host enrollment state, agent
-- certificates, enrollment tokens and per-host update policy. Times are
-- fixed-width RFC 3339 UTC text, so they compare as text. Online state is
-- never stored: it belongs to the controller's live sessions. An M1 local host
-- row becomes pending through the column default.

ALTER TABLE core_hosts ADD COLUMN state TEXT NOT NULL DEFAULT 'pending'
    CHECK (state IN ('pending', 'enrolled', 'revoked'));
ALTER TABLE core_hosts ADD COLUMN enrolled_at TEXT;
ALTER TABLE core_hosts ADD COLUMN last_seen_at TEXT;
ALTER TABLE core_hosts ADD COLUMN agent_version TEXT;
ALTER TABLE core_hosts ADD COLUMN protocol_version INTEGER;
ALTER TABLE core_hosts ADD COLUMN os TEXT;
ALTER TABLE core_hosts ADD COLUMN arch TEXT;

-- One row per issued agent certificate; serials are 128-bit random, stored
-- as lower-case hex without padding.
CREATE TABLE core_agent_certs (
    serial     TEXT PRIMARY KEY,
    host_id    TEXT NOT NULL REFERENCES core_hosts (id) ON DELETE CASCADE,
    not_before TEXT NOT NULL,
    not_after  TEXT NOT NULL,
    revoked_at TEXT,
    created_at TEXT NOT NULL
) STRICT;
CREATE INDEX core_agent_certs_host ON core_agent_certs (host_id);

-- At most one token per host; a new token replaces the row. hash is the
-- SHA-256 of the token's 128-bit secret, which is never stored.
CREATE TABLE core_enroll_tokens (
    host_id     TEXT PRIMARY KEY REFERENCES core_hosts (id) ON DELETE CASCADE,
    hash        BLOB NOT NULL,
    expires_at  TEXT NOT NULL,
    consumed_at TEXT,
    created_at  TEXT NOT NULL
) STRICT;

-- days is the schedule's days joined by ',' in the order mon..sun and
-- time_utc is HH:MM, both null without a schedule; last_window is the id
-- <YYYY-MM-DD>T<HH:MM> of the last window the scheduler claimed.
CREATE TABLE core_host_update (
    host_id         TEXT PRIMARY KEY REFERENCES core_hosts (id) ON DELETE CASCADE,
    channel         TEXT NOT NULL CHECK (channel IN ('stable', 'beta')),
    days            TEXT,
    time_utc        TEXT,
    last_window     TEXT,
    last_rollout_id TEXT
) STRICT;

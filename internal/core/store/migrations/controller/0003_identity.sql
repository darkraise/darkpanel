-- Identity tables (spec sections 8 and 9), read and written only through
-- internal/controller/auth. Times are fixed-width RFC 3339 UTC text, so they
-- compare as text. Sessions are keyed by the hex SHA-256 of the session id and
-- tokens store the hex SHA-256 of their secret; neither secret is stored.
-- Role is checked in code, not here, so a later role needs no table rebuild.

CREATE TABLE core_users (
    id             TEXT PRIMARY KEY,
    username       TEXT NOT NULL UNIQUE,
    role           TEXT NOT NULL,
    state          TEXT NOT NULL CHECK (state IN ('pending', 'active')),
    password_hash  TEXT NOT NULL,
    totp_secret_ct BLOB NOT NULL,
    totp_last_step INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
) STRICT;

CREATE TABLE core_sessions (
    id_hash      TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES core_users (id) ON DELETE CASCADE,
    created_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    source_ip    TEXT NOT NULL,
    user_agent   TEXT NOT NULL
) STRICT;

CREATE INDEX core_sessions_user ON core_sessions (user_id);

CREATE TABLE core_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES core_users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    scope        TEXT NOT NULL CHECK (scope IN ('read', 'operate', 'admin')),
    secret_hash  TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at   TEXT
) STRICT;

CREATE INDEX core_tokens_user ON core_tokens (user_id);

-- Change lifecycle tables (spec section 3, ADR 0004), read and written only
-- through internal/core/lifecycle. Every plan and change row carries record
-- format 1; times are fixed-width RFC 3339 UTC text, so they compare as text.

CREATE TABLE core_plans (
    id           TEXT PRIMARY KEY,
    format       INTEGER NOT NULL DEFAULT 1,
    host_id      TEXT NOT NULL,
    action       TEXT NOT NULL,
    version      INTEGER NOT NULL,
    actor        TEXT NOT NULL,
    input_ct     BLOB,
    input_hash   TEXT NOT NULL,
    state_hash   TEXT NOT NULL,
    diff_json    TEXT NOT NULL,
    guards_json  TEXT NOT NULL,
    confirmation TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    expires_at   TEXT NOT NULL
) STRICT;

CREATE TABLE core_changes (
    id              TEXT PRIMARY KEY,
    format          INTEGER NOT NULL DEFAULT 1,
    plan_id         TEXT NOT NULL UNIQUE REFERENCES core_plans (id),
    host_id         TEXT NOT NULL,
    action          TEXT NOT NULL,
    version         INTEGER NOT NULL,
    class           TEXT NOT NULL,
    compensated_by  TEXT NOT NULL,
    actor           TEXT NOT NULL,
    state           TEXT NOT NULL,
    input_json      TEXT NOT NULL,
    snapshot_ct     BLOB,
    revert_deadline TEXT,
    output_json     TEXT,
    error_code      TEXT,
    error_message   TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
) STRICT;

CREATE INDEX core_changes_state ON core_changes (state);

-- A lock's owner is a change id, or an update transaction id from M2 (ADR 0007).
CREATE TABLE core_locks (
    key   TEXT PRIMARY KEY,
    owner TEXT NOT NULL
) STRICT;

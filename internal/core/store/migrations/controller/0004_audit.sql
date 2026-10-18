-- Audit tables (spec section 11) and the dispatcher's indexes, read and written
-- only through internal/controller/audit and internal/controller/dispatch. Times
-- are fixed-width RFC 3339 UTC text, so they compare as text. The controller
-- never prunes core_audit in v1.

-- One row per login, logout and change event. Controller-side rows have a
-- null host_id and seq; agent rows carry the agent journal's sequence, and the
-- unique index makes their ingestion idempotent (null pairs never collide).
CREATE TABLE core_audit (
    id         TEXT PRIMARY KEY,
    seq        INTEGER,
    at         TEXT NOT NULL,
    actor      TEXT NOT NULL,
    host_id    TEXT,
    action     TEXT NOT NULL,
    version    INTEGER NOT NULL,
    phase      TEXT NOT NULL
        CHECK (phase IN ('apply', 'confirm', 'revert', 'recover', 'interrupt', 'login', 'logout')),
    change_id  TEXT,
    input_json TEXT,
    result     TEXT NOT NULL CHECK (result IN ('ok', 'error')),
    error_code TEXT,
    source     TEXT
) STRICT;

CREATE UNIQUE INDEX core_audit_host_seq ON core_audit (host_id, seq);

-- The highest agent journal sequence stored in core_audit, per host.
CREATE TABLE core_journal_cursor (
    host_id   TEXT PRIMARY KEY,
    acked_seq INTEGER NOT NULL
) STRICT;

-- The host of every agent change, so a change id finds its agent.
CREATE TABLE core_change_index (
    change_id  TEXT PRIMARY KEY,
    host_id    TEXT NOT NULL,
    action     TEXT NOT NULL,
    version    INTEGER NOT NULL,
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX core_change_index_host ON core_change_index (host_id);

-- Every plan the dispatcher made, so Apply runs a plan only for the host and
-- action it was made for (register row 69). host_id is '' for a
-- controller-side plan. Apply sets applied before it runs the plan; an
-- applied plan is kept, so a repeated apply returns its change after a
-- restart, and an unapplied one is deleted once its retention has passed.
CREATE TABLE core_plan_index (
    plan_id    TEXT PRIMARY KEY,
    host_id    TEXT NOT NULL,
    action     TEXT NOT NULL,
    version    INTEGER NOT NULL,
    expires_at TEXT NOT NULL,
    applied    INTEGER NOT NULL DEFAULT 0 CHECK (applied IN (0, 1))
) STRICT;

CREATE INDEX core_plan_index_expires ON core_plan_index (expires_at);

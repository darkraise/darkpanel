-- The DERP server installed on this host; at most one.
CREATE TABLE derp_server (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    change_id       TEXT NOT NULL,
    mode            TEXT NOT NULL CHECK (mode IN ('letsencrypt', 'ip')),
    host_name       TEXT NOT NULL,
    verify_clients  INTEGER NOT NULL CHECK (verify_clients IN (0, 1)),
    derper_version  TEXT NOT NULL,
    binary_sha256   TEXT NOT NULL,
    cert_name       TEXT NOT NULL DEFAULT '',
    cert_not_after  TEXT NOT NULL DEFAULT '',
    installed_at    TEXT NOT NULL,
    updated_at      TEXT NOT NULL
) STRICT;
-- One row per derp.binary.update change: its agent-local transaction.
CREATE TABLE derp_updates (
    change_id     TEXT PRIMARY KEY,
    from_version  TEXT NOT NULL,
    from_sha256   TEXT NOT NULL,
    to_version    TEXT NOT NULL,
    to_sha256     TEXT NOT NULL,
    phase         TEXT NOT NULL CHECK (phase IN ('staged', 'kept', 'swapped', 'restarted', 'done', 'rolling_back', 'rolled_back', 'failed')),
    result        TEXT NOT NULL DEFAULT '',
    updated_at    TEXT NOT NULL
) STRICT;

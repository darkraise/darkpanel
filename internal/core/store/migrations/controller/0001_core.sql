-- Core tables owned by the foundation (spec section 4). Later core tables
-- arrive in their owning part's own numbered file.

CREATE TABLE core_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;

CREATE TABLE core_hosts (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    is_local   INTEGER NOT NULL DEFAULT 0 CHECK (is_local IN (0, 1)),
    created_at TEXT NOT NULL
) STRICT;

CREATE UNIQUE INDEX core_hosts_one_local ON core_hosts (is_local) WHERE is_local = 1;

CREATE TABLE core_module_state (
    host_id    TEXT NOT NULL REFERENCES core_hosts (id) ON DELETE CASCADE,
    module_id  TEXT NOT NULL,
    enabled    INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    available  INTEGER NOT NULL CHECK (available IN (0, 1)),
    reason     TEXT,
    checked_at TEXT NOT NULL,
    PRIMARY KEY (host_id, module_id)
) STRICT;

CREATE TABLE core_ca (
    id         TEXT PRIMARY KEY,
    purpose    TEXT NOT NULL,
    cert_pem   TEXT NOT NULL,
    key_ct     BLOB NOT NULL,
    created_at TEXT NOT NULL
) STRICT;

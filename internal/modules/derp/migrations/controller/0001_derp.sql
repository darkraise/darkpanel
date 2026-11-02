-- One row per host the derp.servers service found running a DERP server.
CREATE TABLE derp_servers (
    host_id         TEXT PRIMARY KEY,
    mode            TEXT NOT NULL CHECK (mode IN ('letsencrypt', 'ip')),
    host_name       TEXT NOT NULL,
    region_id       INTEGER NOT NULL DEFAULT 0,
    region_code     TEXT NOT NULL DEFAULT '',
    derper_version  TEXT NOT NULL,
    cert_name       TEXT NOT NULL DEFAULT '',
    cert_not_after  TEXT NOT NULL DEFAULT '',
    unit_active     INTEGER NOT NULL CHECK (unit_active IN (0, 1)),
    checked_at      TEXT NOT NULL,
    error           TEXT NOT NULL DEFAULT ''
) STRICT;

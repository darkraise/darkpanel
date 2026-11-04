-- The panel module's controller table (spec 8), read and written only through
-- internal/modules/panel. Times are fixed-width RFC 3339 UTC text, so they
-- compare as text.

-- Every exposure of the panel: id is the panel.exposure.add change id and
-- rule_id the firewall rule of its port. A row is gone once it is revoked and
-- its removal_change_id names a completed change; every other row is live, and
-- at most one row is live.
CREATE TABLE panel_exposures (
    id                TEXT PRIMARY KEY,
    via               TEXT NOT NULL CHECK (via IN ('npm', 'tunnel')),
    hostname          TEXT NOT NULL,
    host_id           TEXT NOT NULL,
    tunnel            TEXT NOT NULL DEFAULT '',
    subnet            TEXT NOT NULL,
    gateway           TEXT NOT NULL,
    proxy             TEXT NOT NULL,
    port              INTEGER NOT NULL,
    listener_id       TEXT NOT NULL,
    rule_id           TEXT NOT NULL,
    state             TEXT NOT NULL CHECK (state IN ('open', 'revoked', 'needs_repair')),
    removal_change_id TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
) STRICT;

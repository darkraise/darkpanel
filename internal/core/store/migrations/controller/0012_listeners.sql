-- Runtime listeners (spec 1.10): listeners the controller opens and closes
-- without a restart, each serving one exposure's proxy. The state is
-- durable so that a revocation, committed before anything else, outlives a
-- restart: a listener found revoked at start is never opened again.
CREATE TABLE core_listeners (
    id            TEXT PRIMARY KEY,
    address       TEXT NOT NULL,
    port          INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    allowed_peer  TEXT NOT NULL,
    client_header TEXT NOT NULL CHECK (client_header IN ('X-Darkpanel-Client', 'Cf-Connecting-IP')),
    public_host   TEXT NOT NULL,
    change_id     TEXT NOT NULL,
    state         TEXT NOT NULL CHECK (state IN ('open', 'revoked', 'closed')),
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
) STRICT;

CREATE INDEX core_listeners_state ON core_listeners (state, created_at, id);

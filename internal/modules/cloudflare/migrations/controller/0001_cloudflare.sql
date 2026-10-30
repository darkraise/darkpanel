-- The Cloudflare module's controller tables (spec 7), read and written only
-- through internal/modules/cloudflare. Times are fixed-width RFC 3339 UTC
-- text, so they compare as text; lists are JSON arrays.

-- The connected account: id is the connecting change's id and token_ct the
-- API token sealed with associated data "cloudflare_accounts.token_ct.<id>".
-- At most one row.
CREATE TABLE cloudflare_accounts (
    id                TEXT PRIMARY KEY,
    account_id        TEXT NOT NULL,
    token_ct          BLOB NOT NULL,
    capabilities_json TEXT NOT NULL,
    connected_at      TEXT NOT NULL
) STRICT;
CREATE UNIQUE INDEX cloudflare_accounts_single ON cloudflare_accounts ((1));

-- Every tunnel darkpanel created, by the provider's tunnel id. config_hash
-- is the hash of the ingress configuration last written; drift and
-- checked_at are the last check's result, checked_at NULL until one ran.
CREATE TABLE cloudflare_tunnels (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    change_id   TEXT NOT NULL,
    config_hash TEXT NOT NULL DEFAULT '',
    drift       INTEGER NOT NULL DEFAULT 0 CHECK (drift IN (0, 1)),
    created_at  TEXT NOT NULL,
    checked_at  TEXT
) STRICT;

-- Every public hostname routed through a tunnel. zone_id and record_id are
-- empty until the DNS record is created; phase is the step the hostname's
-- add or remove reached, and health with checked_at the last check's result.
CREATE TABLE cloudflare_hostnames (
    hostname      TEXT PRIMARY KEY,
    tunnel_id     TEXT NOT NULL REFERENCES cloudflare_tunnels (id),
    service       TEXT NOT NULL,
    no_tls_verify INTEGER NOT NULL DEFAULT 0 CHECK (no_tls_verify IN (0, 1)),
    zone_id       TEXT NOT NULL DEFAULT '',
    record_id     TEXT NOT NULL DEFAULT '',
    phase         TEXT NOT NULL CHECK (phase IN ('adding_rule', 'adding_record', 'active', 'removing_record', 'removing_rule')),
    change_id     TEXT NOT NULL,
    health        TEXT NOT NULL DEFAULT 'unknown' CHECK (health IN ('ok', 'rule_missing', 'record_missing', 'record_changed', 'unknown')),
    checked_at    TEXT,
    updated_at    TEXT NOT NULL
) STRICT;

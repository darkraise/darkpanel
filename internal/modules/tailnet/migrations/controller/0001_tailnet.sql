-- The tailnet module's controller tables (spec 4.2), read and written only
-- through internal/modules/tailnet. Times are fixed-width RFC 3339 UTC
-- text, so they compare as text; lists are JSON arrays.

-- The connected account: id is the connecting change's id and
-- client_secret_ct the OAuth client secret sealed with associated data
-- "tailnet_accounts.client_secret_ct.<id>". At most one row.
CREATE TABLE tailnet_accounts (
    id                TEXT PRIMARY KEY,
    tailnet           TEXT NOT NULL,
    client_id         TEXT NOT NULL,
    client_secret_ct  BLOB NOT NULL,
    tags_allowed_json TEXT NOT NULL,
    capabilities_json TEXT NOT NULL,
    connected_at      TEXT NOT NULL
) STRICT;
CREATE UNIQUE INDEX tailnet_accounts_one ON tailnet_accounts ((1));

-- Every auth key darkpanel requested, by its description. key_id is empty
-- until the provider answers it.
CREATE TABLE tailnet_keys (
    description    TEXT PRIMARY KEY,
    kind           TEXT NOT NULL CHECK (kind IN ('change', 'enroll')),
    owner_id       TEXT NOT NULL,
    requested_at   TEXT NOT NULL,
    expiry_seconds INTEGER NOT NULL,
    tags_json      TEXT NOT NULL,
    key_id         TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL CHECK (state IN ('requested', 'live', 'uncertain', 'used', 'retired', 'deleted')),
    updated_at     TEXT NOT NULL
) STRICT;

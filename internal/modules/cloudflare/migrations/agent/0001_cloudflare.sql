-- The Cloudflare module's agent tables (spec 7.2, 7.3), read and written only
-- through internal/modules/cloudflare. Times are fixed-width RFC 3339 UTC
-- text, so they compare as text.

-- One row per cloudflared connector on this host, by the tunnel's id. The
-- tunnel token is sealed with associated data
-- "cloudflare_connectors.token_ct.<tunnel id>".
CREATE TABLE cloudflare_connectors (
    tunnel_id    TEXT PRIMARY KEY,
    container_id TEXT NOT NULL,
    token_ct     BLOB NOT NULL,
    change_id    TEXT NOT NULL,
    created_at   TEXT NOT NULL
) STRICT;

-- Who needs a tunnel's connector attached to a network.
CREATE TABLE cloudflare_attachments (
    tunnel_id   TEXT NOT NULL,
    network     TEXT NOT NULL,
    claimant    TEXT NOT NULL,
    change_id   TEXT NOT NULL,
    preexisting INTEGER NOT NULL CHECK (preexisting IN (0, 1)),
    PRIMARY KEY (tunnel_id, network, claimant)
) STRICT;

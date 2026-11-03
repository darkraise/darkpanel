-- The Nginx Proxy Manager module's agent tables (spec 6), read and written
-- only through internal/modules/npm. Times are fixed-width RFC 3339 UTC text,
-- so they compare as text.

-- The one instance on this host. The admin password is sealed with the agent's
-- sealer (associated data npm_instances.password_ct.1).
CREATE TABLE npm_instances (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    container_id TEXT NOT NULL,
    image       TEXT NOT NULL,
    admin_email TEXT NOT NULL,
    password_ct BLOB NOT NULL,
    public      TEXT NOT NULL DEFAULT 'no' CHECK (public IN ('no', 'confirmed')),
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
) STRICT;

-- The objects an exposure of the owner's admin UI or of the panel owns.
-- exposure is the exposure's host name.
CREATE TABLE npm_reserved (
    kind      TEXT NOT NULL CHECK (kind IN ('proxy_host', 'access_list', 'certificate')),
    object_id INTEGER NOT NULL,
    owner     TEXT NOT NULL CHECK (owner IN ('npm.admin.expose', 'panel.exposure.add')),
    exposure  TEXT NOT NULL,
    change_id TEXT NOT NULL,
    PRIMARY KEY (kind, object_id)
) STRICT;

-- Who needs the NPM container attached to a network.
CREATE TABLE npm_attachments (
    network     TEXT NOT NULL,
    claimant    TEXT NOT NULL,
    change_id   TEXT NOT NULL,
    preexisting INTEGER NOT NULL CHECK (preexisting IN (0, 1)),
    PRIMARY KEY (network, claimant)
) STRICT;

-- One row per npm.instance.upgrade change: its agent-local transaction.
-- config is the original container's create configuration as JSON, and
-- archives is a JSON array of {volume, file, sha256}.
CREATE TABLE npm_upgrades (
    change_id  TEXT PRIMARY KEY,
    from_image TEXT NOT NULL,
    to_image   TEXT NOT NULL,
    config     TEXT NOT NULL,
    archives   TEXT NOT NULL DEFAULT '[]',
    phase      TEXT NOT NULL CHECK (phase IN ('recorded', 'stopped', 'archiving', 'archived', 'recreating', 'waiting', 'done', 'rolled_back', 'failed')),
    result     TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL
) STRICT;

-- Identities whose effect was resolved skipped: an object that appears later
-- under one of them is shown as unclaimed.
CREATE TABLE npm_watch (
    kind      TEXT NOT NULL,
    identity  TEXT NOT NULL,
    change_id TEXT NOT NULL,
    seq       INTEGER NOT NULL,
    PRIMARY KEY (kind, identity)
) STRICT;

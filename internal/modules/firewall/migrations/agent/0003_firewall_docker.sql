-- The Docker integration's logical state (spec 2.6), read and written only
-- through internal/modules/firewall. Times are fixed-width RFC 3339 UTC
-- text, so they compare as text.
--
-- firewall_settings holds at most one row: the integration flag and the
-- JSON of its settings. In firewall_container_rules, host_addr and source
-- are the empty string for "every address" and "any source". A cleanup
-- intent exists from the cleanup's first step until it verified. A
-- foreign-rule record exists from before a transition until DOCKER-USER
-- was reloaded after it.
CREATE TABLE firewall_settings (
    id       INTEGER PRIMARY KEY CHECK (id = 1),
    docker   INTEGER NOT NULL DEFAULT 0 CHECK (docker IN (0, 1)),
    settings TEXT NOT NULL
) STRICT;
CREATE TABLE firewall_container_rules (
    id         TEXT PRIMARY KEY,
    selector   TEXT NOT NULL,
    host_addr  TEXT NOT NULL,
    host_port  INTEGER NOT NULL CHECK (host_port BETWEEN 1 AND 65535),
    proto      TEXT NOT NULL CHECK (proto IN ('tcp', 'udp')),
    kind       TEXT NOT NULL CHECK (kind IN ('allow', 'deny')),
    source     TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (selector, host_addr, host_port, proto, kind, source)
) STRICT;
CREATE TABLE firewall_docker_cleanups (
    owner      TEXT PRIMARY KEY,
    started_at TEXT NOT NULL
) STRICT;
CREATE TABLE firewall_docker_foreign (
    owner      TEXT PRIMARY KEY,
    record     TEXT NOT NULL,
    created_at TEXT NOT NULL
) STRICT;

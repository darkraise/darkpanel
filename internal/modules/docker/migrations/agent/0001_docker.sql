-- The Compose projects the docker module has seen (spec 3, Discovery), read
-- and written only through internal/modules/docker. Times are fixed-width
-- RFC 3339 UTC text, so they compare as text; the *_json columns hold JSON
-- arrays of strings.
CREATE TABLE docker_projects (
    name           TEXT PRIMARY KEY,
    directory      TEXT NOT NULL,
    files_json     TEXT NOT NULL,
    env_files_json TEXT NOT NULL,
    profiles_json  TEXT NOT NULL DEFAULT '[]',
    run_hash       TEXT NOT NULL DEFAULT '',
    first_seen     TEXT NOT NULL,
    last_run       TEXT
) STRICT;

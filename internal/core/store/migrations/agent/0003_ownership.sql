-- The ownership manifest (ADR 0009): every file and marked block Darkpanel
-- wrote on this host, which darkpanel uninstall reverses. A whole file has an
-- empty block_id.

CREATE TABLE core_owned_files (
    path       TEXT NOT NULL,
    module     TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('file', 'block')),
    block_id   TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (path, module, block_id),
    CHECK ((kind = 'file') = (block_id = ''))
) STRICT;

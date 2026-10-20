-- Uninstall also removes the directories, users and units Darkpanel created
-- (spec section 7), so the manifest gains those kinds. SQLite cannot change a
-- CHECK constraint in place, so the table is rebuilt. Only a marked block has
-- a block id.

CREATE TABLE core_owned_files_new (
    path       TEXT NOT NULL,
    module     TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('file', 'block', 'dir', 'user', 'unit')),
    block_id   TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (path, module, block_id),
    CHECK ((kind = 'block') = (block_id <> ''))
) STRICT;

INSERT INTO core_owned_files_new (path, module, kind, block_id, created_at)
    SELECT path, module, kind, block_id, created_at FROM core_owned_files;

DROP TABLE core_owned_files;

ALTER TABLE core_owned_files_new RENAME TO core_owned_files;

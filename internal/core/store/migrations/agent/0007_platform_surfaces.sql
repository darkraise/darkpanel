-- A background task's changes are marked system (spec 1.4), so rollbacks and
-- snapshot retention can tell them apart. Only TaskContext.Apply sets it.
ALTER TABLE core_changes ADD COLUMN system INTEGER NOT NULL DEFAULT 0 CHECK (system IN (0, 1));

-- Uninstall removes the managed and service-user directories a module
-- created (spec 1.8), so the manifest records those kinds too. SQLite cannot
-- change a CHECK constraint in place, so the table is rebuilt, keeping every
-- column, row and the primary key. Only a marked block has a block id.

CREATE TABLE core_owned_files_new (
    path       TEXT NOT NULL,
    module     TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('file', 'block', 'dir', 'user', 'unit', 'managed-dir', 'service-dir')),
    block_id   TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (path, module, block_id),
    CHECK ((kind = 'block') = (block_id <> ''))
) STRICT;

INSERT INTO core_owned_files_new (path, module, kind, block_id, created_at)
    SELECT path, module, kind, block_id, created_at FROM core_owned_files;

DROP TABLE core_owned_files;

ALTER TABLE core_owned_files_new RENAME TO core_owned_files;

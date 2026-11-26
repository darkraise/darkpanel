-- The console file manager's operations (M11 spec section 5): a row is
-- written, with the temporary file of a write, before the first byte, and
-- gets its result once the result is journaled. A row without a result at
-- agent start is an operation a crash cut off: it is journaled as
-- interrupted and its temporary file removed.
CREATE TABLE console_file_ops (
    id TEXT PRIMARY KEY,
    op TEXT NOT NULL,
    path TEXT NOT NULL,
    tmp_path TEXT,
    actor TEXT NOT NULL,
    source TEXT NOT NULL,
    size INTEGER,
    sha256 TEXT,
    started_at TEXT NOT NULL,
    result TEXT
) STRICT;

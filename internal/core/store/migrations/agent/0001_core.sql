-- Core tables owned by the foundation (spec section 4). Later core tables
-- arrive in their owning part's own numbered file.

CREATE TABLE core_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;

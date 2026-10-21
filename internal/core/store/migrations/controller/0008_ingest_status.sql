-- Journal ingestion health per host, read and written only through
-- internal/controller/audit and read by core.host.list. since is fixed-width
-- RFC 3339 UTC text: when the current state began. error is NULL while
-- ingestion succeeds. A host without a row has not been ingested yet.

CREATE TABLE core_ingest_status (
    host_id TEXT PRIMARY KEY REFERENCES core_hosts (id) ON DELETE CASCADE,
    since TEXT NOT NULL,
    error TEXT
) STRICT;

-- Journal epochs (spec section 5): an agent's journal sequence starts again
-- at 1 when agent.db or its journal table is recreated, under a new epoch the
-- agent reports in Hello. The ingestion cursor and the ingested rows are
-- therefore keyed by epoch. Rows stored before this migration, and
-- controller-side rows, carry the empty epoch.

ALTER TABLE core_journal_cursor ADD COLUMN epoch TEXT NOT NULL DEFAULT '';

ALTER TABLE core_audit ADD COLUMN epoch TEXT NOT NULL DEFAULT '';

DROP INDEX core_audit_host_seq;

CREATE UNIQUE INDEX core_audit_host_epoch_seq ON core_audit (host_id, epoch, seq);

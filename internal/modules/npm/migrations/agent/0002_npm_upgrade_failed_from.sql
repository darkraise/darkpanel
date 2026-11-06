-- The phase an upgrade's rollback was in when it failed. A later rollback of the same row takes the same branch:
-- before the original container is removed it only starts that container again, and afterwards it restores the
-- original from the archives. Empty for a row that never failed, and for one that failed before this column existed.
ALTER TABLE npm_upgrades ADD COLUMN failed_from TEXT NOT NULL DEFAULT '' CHECK (failed_from IN ('', 'recorded', 'stopped', 'archiving', 'archived', 'recreating', 'waiting', 'done'));

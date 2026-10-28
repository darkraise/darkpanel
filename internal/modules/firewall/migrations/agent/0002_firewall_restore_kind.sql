-- The kind of a restore in progress (spec 2.5). An emergency restore, the
-- connectivity exception, keeps the record of what it overwrote beside its
-- restore record, so that a restore a crash cut off still ends as an
-- override when it is finished. override is NULL exactly for a normal
-- restore; a row written before this migration is a normal restore.
ALTER TABLE firewall_restores ADD COLUMN kind TEXT NOT NULL DEFAULT 'normal' CHECK (kind IN ('normal', 'emergency'));
ALTER TABLE firewall_restores ADD COLUMN override TEXT CHECK ((override IS NOT NULL) = (kind = 'emergency'));

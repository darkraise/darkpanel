-- The client address of the principal behind each journal entry (spec
-- section 11: audit rows carry the source). System actors, such as the revert
-- timer and recovery, record an empty source.

ALTER TABLE core_journal ADD COLUMN source TEXT NOT NULL DEFAULT '';

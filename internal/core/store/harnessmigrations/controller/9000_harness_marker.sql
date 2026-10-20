-- Applied only by a harnessmigration build: the harness reads this row to see
-- that a newer schema was applied, then that an older binary runs on it.
CREATE TABLE harness_marker (note TEXT NOT NULL) STRICT;
INSERT INTO harness_marker (note) VALUES ('written by the harnessmigration build');

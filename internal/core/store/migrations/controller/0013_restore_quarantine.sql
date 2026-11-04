-- Restore quarantine (spec 10): a restored database marks its non-terminal
-- records quarantined and remembers the state each had. core_changes gains
-- quarantined_from. core_rollouts.state also accepts quarantined and the
-- table gains quarantined_from; SQLite cannot change a CHECK constraint in
-- place, so core_rollouts is rebuilt with the wider list, keeping every
-- column, row and index.
--
-- core_rollout_hosts references core_rollouts with ON DELETE CASCADE and
-- the connection enforces foreign keys, so dropping core_rollouts while the
-- old child exists would delete every host row. Both tables are therefore
-- copied into new tables first, the old child is dropped before its parent,
-- and the renames follow; SQLite rewrites the new child's reference to the
-- renamed parent.

ALTER TABLE core_changes ADD COLUMN quarantined_from TEXT;

CREATE TABLE core_rollouts_new (
    id               TEXT PRIMARY KEY,
    version          TEXT NOT NULL,
    channel          TEXT NOT NULL CHECK (channel IN ('stable', 'beta')),
    state            TEXT NOT NULL
        CHECK (state IN ('running', 'draining', 'quarantined', 'succeeded', 'failed', 'cancelled')),
    outcome          TEXT CHECK (outcome IN ('failed', 'cancelled')),
    error_code       TEXT,
    error_message    TEXT,
    change_id        TEXT,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    quarantined_from TEXT,
    CHECK (state <> 'draining' OR outcome IS NOT NULL)
) STRICT;

INSERT INTO core_rollouts_new (id, version, channel, state, outcome, error_code, error_message, change_id,
                               created_at, updated_at)
    SELECT id, version, channel, state, outcome, error_code, error_message, change_id, created_at, updated_at
    FROM core_rollouts;

CREATE TABLE core_rollout_hosts_new (
    rollout_id    TEXT NOT NULL REFERENCES core_rollouts_new (id) ON DELETE CASCADE,
    host_id       TEXT NOT NULL,
    position      INTEGER NOT NULL CHECK (position >= 0),
    state         TEXT NOT NULL
        CHECK (state IN ('waiting', 'updating', 'committed', 'failed', 'skipped')),
    plan_id       TEXT,
    apply_sent_at TEXT,
    operation_id  TEXT,
    error_code    TEXT,
    error_message TEXT,
    followed_at   TEXT,
    PRIMARY KEY (rollout_id, host_id),
    UNIQUE (rollout_id, position)
) STRICT;

INSERT INTO core_rollout_hosts_new (rollout_id, host_id, position, state, plan_id, apply_sent_at, operation_id,
                                    error_code, error_message, followed_at)
    SELECT rollout_id, host_id, position, state, plan_id, apply_sent_at, operation_id, error_code, error_message,
           followed_at
    FROM core_rollout_hosts;

DROP TABLE core_rollout_hosts;
DROP TABLE core_rollouts;

ALTER TABLE core_rollouts_new RENAME TO core_rollouts;
ALTER TABLE core_rollout_hosts_new RENAME TO core_rollout_hosts;

-- Every active rollout has the same index key, so the database itself
-- refuses a second one (rollout.in_progress). A quarantined rollout is not
-- active.
CREATE UNIQUE INDEX core_rollouts_one_active ON core_rollouts ((1))
    WHERE state IN ('running', 'draining');
CREATE INDEX core_rollouts_created ON core_rollouts (created_at);

CREATE UNIQUE INDEX core_rollout_hosts_one_updating ON core_rollout_hosts (rollout_id)
    WHERE state = 'updating';

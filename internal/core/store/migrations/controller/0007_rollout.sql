-- Fleet rollouts (M2 spec section 8, Fleet rollout), read and written only
-- through internal/controller/rollout. Times are fixed-width RFC 3339 UTC
-- text, so they compare as text.

-- outcome is the state a draining rollout ends in once its host's update is
-- over: failed after a timeout, cancelled after core.update.rollout-cancel.
-- change_id is the core.update.rollout change that created the rollout; a
-- scheduled rollout is not a change and has none.
CREATE TABLE core_rollouts (
    id            TEXT PRIMARY KEY,
    version       TEXT NOT NULL,
    channel       TEXT NOT NULL CHECK (channel IN ('stable', 'beta')),
    state         TEXT NOT NULL
        CHECK (state IN ('running', 'draining', 'succeeded', 'failed', 'cancelled')),
    outcome       TEXT CHECK (outcome IN ('failed', 'cancelled')),
    error_code    TEXT,
    error_message TEXT,
    change_id     TEXT,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    CHECK (state <> 'draining' OR outcome IS NOT NULL)
) STRICT;

-- Every active rollout has the same index key, so the database itself
-- refuses a second one (rollout.in_progress).
CREATE UNIQUE INDEX core_rollouts_one_active ON core_rollouts ((1))
    WHERE state IN ('running', 'draining');
CREATE INDEX core_rollouts_created ON core_rollouts (created_at);

-- The hosts of a rollout, updated one at a time in position order.
-- followed_at is when the driver stored the host's update operation and
-- began polling it, for the ten-minute limit.
-- host_id has no foreign key, so a finished rollout keeps naming a host
-- that was deleted later.
CREATE TABLE core_rollout_hosts (
    rollout_id    TEXT NOT NULL REFERENCES core_rollouts (id) ON DELETE CASCADE,
    host_id       TEXT NOT NULL,
    position      INTEGER NOT NULL CHECK (position >= 0),
    state         TEXT NOT NULL
        CHECK (state IN ('waiting', 'updating', 'committed', 'failed', 'skipped')),
    plan_id       TEXT,
    operation_id  TEXT,
    error_code    TEXT,
    error_message TEXT,
    followed_at   TEXT,
    PRIMARY KEY (rollout_id, host_id),
    UNIQUE (rollout_id, position)
) STRICT;

CREATE UNIQUE INDEX core_rollout_hosts_one_updating ON core_rollout_hosts (rollout_id)
    WHERE state = 'updating';

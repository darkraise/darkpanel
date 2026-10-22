-- The lifecycle core (spec sections 1.2 to 1.3c), read and written only
-- through internal/core/lifecycle: who owns an unfinished change and what
-- repaired it, the call context and repair target of a plan, the lock epochs
-- that bound the undo window, the effects journal, and the results of task
-- units. The migration only adds columns, tables and indexes; existing rows
-- keep their values and the new columns start at their defaults. Times are
-- fixed-width RFC 3339 UTC text, so they compare as text.

ALTER TABLE core_changes ADD COLUMN parent_change_id TEXT;
ALTER TABLE core_changes ADD COLUMN recovery_owner TEXT NOT NULL DEFAULT '';
ALTER TABLE core_changes ADD COLUMN repaired_by TEXT;
ALTER TABLE core_changes ADD COLUMN repair_outcome TEXT;
ALTER TABLE core_changes ADD COLUMN recovery_retry INTEGER NOT NULL DEFAULT 0 CHECK (recovery_retry IN (0, 1));
ALTER TABLE core_changes ADD COLUMN private_output_ct BLOB;
ALTER TABLE core_changes ADD COLUMN private_output_at TEXT;
ALTER TABLE core_changes ADD COLUMN lock_epochs TEXT;      -- JSON object {"<key>": <epoch>}
ALTER TABLE core_changes ADD COLUMN override_json TEXT;
ALTER TABLE core_changes ADD COLUMN error_cause TEXT;      -- ChangeError.Cause
CREATE INDEX core_changes_parent ON core_changes (parent_change_id);

ALTER TABLE core_plans ADD COLUMN repairs TEXT;            -- the change id a repair plan repairs
ALTER TABLE core_plans ADD COLUMN call TEXT;               -- JSON wire.CallContext without repairOf
ALTER TABLE core_plans ADD COLUMN retired_at TEXT;         -- set by retire; a retired plan is never applied
CREATE INDEX core_plans_repairs ON core_plans (repairs);

CREATE TABLE core_lock_epochs (
    key   TEXT PRIMARY KEY,
    epoch INTEGER NOT NULL
) STRICT;

CREATE TABLE core_effects (
    change_id             TEXT NOT NULL REFERENCES core_changes (id),
    seq                   INTEGER NOT NULL CHECK (seq >= 1),
    kind                  TEXT NOT NULL CHECK (kind IN ('create', 'modify', 'delete')),
    resource              TEXT NOT NULL,
    recovery_json         TEXT,
    local                 INTEGER NOT NULL CHECK (local IN (0, 1)),
    inverse_local         INTEGER NOT NULL CHECK (inverse_local IN (0, 1)),
    commit_wait_ns        INTEGER NOT NULL DEFAULT 0,
    commit_by             TEXT,
    inverse_recovery_json TEXT,
    undo_resource         TEXT NOT NULL DEFAULT '',
    before_json           TEXT NOT NULL,
    after_json            TEXT,
    post_json             TEXT,
    undo_to_json          TEXT,
    current_json          TEXT,
    state                 TEXT NOT NULL CHECK (state IN ('intended', 'made', 'skipped', 'unresolved',
                                                         'undoing', 'undone', 'accepted', 'abandoned')),
    reason                TEXT NOT NULL DEFAULT '' CHECK (reason IN ('', 'uncertain', 'ambiguous', 'conflict')),
    resolution            TEXT NOT NULL DEFAULT '' CHECK (resolution IN ('', 'accept', 'reconcile')),
    resolved_by           TEXT NOT NULL DEFAULT '',
    resolved_at           TEXT,
    secret_ct             BLOB,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    PRIMARY KEY (change_id, seq)
) STRICT;
CREATE INDEX core_effects_state ON core_effects (state);

-- One row per transient unit a task action started (op apply, undo or
-- recover; attempt 1, 2, ... per change and op). The agent task process
-- writes its outcome here; output_ct is sealed with associated data
-- core_task_runs.output_ct.<change id>.<op>.<attempt>.
CREATE TABLE core_task_runs (
    change_id     TEXT NOT NULL REFERENCES core_changes (id),
    op            TEXT NOT NULL CHECK (op IN ('apply', 'undo', 'recover')),
    attempt       INTEGER NOT NULL CHECK (attempt >= 1),
    state         TEXT NOT NULL CHECK (state IN ('starting', 'running', 'succeeded', 'failed', 'cancelled')),
    output_ct     BLOB,
    error_code    TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    pid           INTEGER,
    started_at    TEXT NOT NULL,
    finished_at   TEXT,
    PRIMARY KEY (change_id, op, attempt)
) STRICT;

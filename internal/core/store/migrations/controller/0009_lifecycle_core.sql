-- The lifecycle core (spec sections 1.2 to 1.3c), read and written only
-- through internal/core/lifecycle: who owns an unfinished change and what
-- repaired it, the call context and repair target of a plan, the lock epochs
-- that bound the undo window, the effects journal of controller-side
-- changes, and the step logs of composites. The migration only adds
-- columns, tables and indexes; existing rows keep their values and the new
-- columns start at their defaults. Times are fixed-width RFC 3339 UTC text,
-- so they compare as text.

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

-- The dispatcher records the target of a repair plan it relays, so it can
-- refuse the apply of a repair plan whose target is a releasing composite's
-- child.
ALTER TABLE core_plan_index ADD COLUMN repairs TEXT;       -- set when the dispatcher relays a repair plan

-- A composite's progress: the direction it runs in, the ordinal of its
-- cursor step, and when its steps phase ends.
CREATE TABLE core_composites (
    change_id      TEXT PRIMARY KEY REFERENCES core_changes (id),
    direction      TEXT NOT NULL CHECK (direction IN ('forward', 'unwind')),
    cursor         INTEGER NOT NULL,
    steps_deadline TEXT NOT NULL
) STRICT;

-- One row per step of a composite, written in its intent transaction.
-- input_ct and output_ct are sealed with associated data
-- core_composite_steps.input_ct.<composite id>.<step id> and
-- core_composite_steps.output_ct.<composite id>.<step id>.
CREATE TABLE core_composite_steps (
    change_id                TEXT NOT NULL REFERENCES core_changes (id),
    step_id                  TEXT NOT NULL,
    ordinal                  INTEGER NOT NULL,
    host_id                  TEXT NOT NULL,
    action                   TEXT NOT NULL,              -- "<name>/v<version>"
    after_json               TEXT NOT NULL DEFAULT '[]',
    input_template           TEXT NOT NULL,              -- after ${input...} resolution
    state                    TEXT NOT NULL CHECK (state IN ('pending', 'planning', 'planned', 'applying', 'applied',
                                                            'confirming', 'confirmed', 'read', 'undoing', 'undone',
                                                            'released')),
    attempt                  INTEGER NOT NULL DEFAULT 0,
    child_plan_id            TEXT,
    child_change_id          TEXT,
    earlier_child_change_ids TEXT NOT NULL DEFAULT '[]',
    input_ct                 BLOB,                       -- sealed resolved input
    output_ct                BLOB,                       -- sealed unredacted output (and a read step's result)
    child_deadline           TEXT,
    error_code               TEXT,
    error_message            TEXT,
    updated_at               TEXT NOT NULL,
    PRIMARY KEY (change_id, step_id),
    UNIQUE (change_id, ordinal)
) STRICT;
CREATE INDEX core_composite_steps_child ON core_composite_steps (child_change_id);

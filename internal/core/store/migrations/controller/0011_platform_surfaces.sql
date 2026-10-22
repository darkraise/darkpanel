-- A background task's changes are marked system (spec 1.4), on the
-- controller as on the agent. Only TaskContext.Apply sets it.
ALTER TABLE core_changes ADD COLUMN system INTEGER NOT NULL DEFAULT 0 CHECK (system IN (0, 1));

-- The controller's change watcher asks every second for the changes updated
-- since its last poll (spec 1.6). Agent changes reach the controller through
-- the audit ingester, so agent.db needs no such index.
CREATE INDEX core_changes_updated ON core_changes (updated_at);

-- A reveal (spec 1.14) and an aborted uninstall (spec 1.11) are journaled
-- with the phases reveal and uninstall-abort, on the controller engine and in
-- the agent journals the controller ingests. SQLite cannot change a CHECK
-- constraint in place, so the table is rebuilt with the wider list, keeping
-- every column, row and index. The previous binary reads phase as a plain
-- string (ADR 0005).

CREATE TABLE core_audit_new (
    id         TEXT PRIMARY KEY,
    seq        INTEGER,
    at         TEXT NOT NULL,
    actor      TEXT NOT NULL,
    host_id    TEXT,
    action     TEXT NOT NULL,
    version    INTEGER NOT NULL,
    phase      TEXT NOT NULL
        CHECK (phase IN ('apply', 'confirm', 'revert', 'recover', 'interrupt', 'login', 'logout',
                         'undo', 'resolve', 'release', 'revert-watchdog', 'reveal', 'uninstall-abort')),
    change_id  TEXT,
    input_json TEXT,
    result     TEXT NOT NULL CHECK (result IN ('ok', 'error', 'override')),
    error_code TEXT,
    source     TEXT,
    epoch      TEXT NOT NULL DEFAULT ''
) STRICT;

INSERT INTO core_audit_new (id, seq, at, actor, host_id, action, version, phase, change_id, input_json, result,
                            error_code, source, epoch)
    SELECT id, seq, at, actor, host_id, action, version, phase, change_id, input_json, result, error_code, source,
           epoch
    FROM core_audit;

DROP TABLE core_audit;

ALTER TABLE core_audit_new RENAME TO core_audit;

CREATE UNIQUE INDEX core_audit_host_epoch_seq ON core_audit (host_id, epoch, seq);

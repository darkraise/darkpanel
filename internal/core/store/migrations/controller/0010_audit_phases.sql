-- The lifecycle core journals the phases undo, resolve, release and
-- revert-watchdog and the result override (spec 1.2 and 1.3), on the
-- controller engine and in the agent journals the controller ingests.
-- SQLite cannot change a CHECK constraint in place, so the table is rebuilt
-- with the wider lists, keeping every column, row and index. The previous
-- binary reads phase and result as plain strings (ADR 0005).

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
                         'undo', 'resolve', 'release', 'revert-watchdog')),
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

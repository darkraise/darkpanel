-- The firewall's logical snapshots (spec 2.5), read and written only
-- through internal/modules/firewall. Times are fixed-width RFC 3339 UTC
-- text, so they compare as text.
--
-- A snapshot is captured before its change exists, so change_id is NULL
-- until the change's Apply or Revert attaches it. entries holds UFW's state
-- files as core.RestoreEntry values (Data base64), state the logical state
-- (live sysctl values, the recorded guard context, a repair's loaded-state
-- before-image, the Docker integration flag), hashes each file's hash at
-- capture and post_hashes each file's hash right after the change applied.
CREATE TABLE firewall_snapshots (
    id          TEXT PRIMARY KEY,
    created_at  TEXT NOT NULL,
    change_id   TEXT,
    actor       TEXT NOT NULL,
    system      INTEGER NOT NULL DEFAULT 0 CHECK (system IN (0, 1)),
    pinned      INTEGER NOT NULL DEFAULT 0 CHECK (pinned IN (0, 1)),
    baseline    INTEGER NOT NULL DEFAULT 0 CHECK (baseline IN (0, 1)),
    entries     TEXT NOT NULL,
    state       TEXT NOT NULL,
    hashes      TEXT NOT NULL,
    post_hashes TEXT
) STRICT;
CREATE INDEX firewall_snapshots_retention ON firewall_snapshots (system, pinned, baseline, created_at);
CREATE INDEX firewall_snapshots_change ON firewall_snapshots (change_id);
CREATE UNIQUE INDEX firewall_snapshots_baseline ON firewall_snapshots (baseline) WHERE baseline = 1;

-- A restore in progress: written before the first file and deleted after
-- the restore verified, so a restore cut off by a crash is finished by
-- restoring the same snapshot with the same UFW transition. At most one row
-- exists, because a new restore supersedes an unfinished one.
CREATE TABLE firewall_restores (
    snapshot_id TEXT PRIMARY KEY REFERENCES firewall_snapshots (id),
    transition  TEXT NOT NULL CHECK (transition IN ('none', 'reload', 'enable', 'disable')),
    started_at  TEXT NOT NULL
) STRICT;

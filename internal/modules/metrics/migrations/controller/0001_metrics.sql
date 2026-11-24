-- The metrics module's controller tables (M10 spec section 5.2), read and
-- written only through internal/modules/metrics/controller. ts is the start
-- of a minute or an hour as fixed-width RFC 3339 UTC text with whole seconds
-- (2026-11-24T12:00:00Z), so it compares as text; bytes are integers, CPU
-- percent, load and bytes per second reals. Backfill writes with INSERT OR
-- IGNORE, so overlapping polls store each minute once.

CREATE TABLE metrics_minute (
    host_id    TEXT NOT NULL REFERENCES core_hosts (id) ON DELETE CASCADE,
    ts         TEXT NOT NULL,
    cpu        REAL NOT NULL,
    mem_used   INTEGER NOT NULL,
    mem_total  INTEGER NOT NULL,
    swap_used  INTEGER NOT NULL,
    swap_total INTEGER NOT NULL,
    load1      REAL NOT NULL,
    load5      REAL NOT NULL,
    load15     REAL NOT NULL,
    PRIMARY KEY (host_id, ts)
) STRICT;
CREATE INDEX metrics_minute_ts ON metrics_minute (ts);

CREATE TABLE metrics_minute_fs (
    host_id TEXT NOT NULL REFERENCES core_hosts (id) ON DELETE CASCADE,
    ts      TEXT NOT NULL,
    mount   TEXT NOT NULL,
    used    INTEGER NOT NULL,
    total   INTEGER NOT NULL,
    PRIMARY KEY (host_id, ts, mount)
) STRICT;
CREATE INDEX metrics_minute_fs_ts ON metrics_minute_fs (ts);

CREATE TABLE metrics_minute_net (
    host_id TEXT NOT NULL REFERENCES core_hosts (id) ON DELETE CASCADE,
    ts      TEXT NOT NULL,
    iface   TEXT NOT NULL,
    rx_bps  REAL NOT NULL,
    tx_bps  REAL NOT NULL,
    PRIMARY KEY (host_id, ts, iface)
) STRICT;
CREATE INDEX metrics_minute_net_ts ON metrics_minute_net (ts);

-- The hour tables hold each completed hour's averages, ts its start.
CREATE TABLE metrics_hour (
    host_id    TEXT NOT NULL REFERENCES core_hosts (id) ON DELETE CASCADE,
    ts         TEXT NOT NULL,
    cpu        REAL NOT NULL,
    mem_used   INTEGER NOT NULL,
    mem_total  INTEGER NOT NULL,
    swap_used  INTEGER NOT NULL,
    swap_total INTEGER NOT NULL,
    load1      REAL NOT NULL,
    load5      REAL NOT NULL,
    load15     REAL NOT NULL,
    PRIMARY KEY (host_id, ts)
) STRICT;
CREATE INDEX metrics_hour_ts ON metrics_hour (ts);

CREATE TABLE metrics_hour_fs (
    host_id TEXT NOT NULL REFERENCES core_hosts (id) ON DELETE CASCADE,
    ts      TEXT NOT NULL,
    mount   TEXT NOT NULL,
    used    INTEGER NOT NULL,
    total   INTEGER NOT NULL,
    PRIMARY KEY (host_id, ts, mount)
) STRICT;
CREATE INDEX metrics_hour_fs_ts ON metrics_hour_fs (ts);

CREATE TABLE metrics_hour_net (
    host_id TEXT NOT NULL REFERENCES core_hosts (id) ON DELETE CASCADE,
    ts      TEXT NOT NULL,
    iface   TEXT NOT NULL,
    rx_bps  REAL NOT NULL,
    tx_bps  REAL NOT NULL,
    PRIMARY KEY (host_id, ts, iface)
) STRICT;
CREATE INDEX metrics_hour_net_ts ON metrics_hour_net (ts);

-- One row per polled host. last_error_code is a short code (an error code
-- such as module.unavailable, or metrics.clock_skew) while the last poll
-- failed and NULL once one succeeds; clock_skew_s is the agent clock less
-- the controller's at the last answer, in whole seconds.
CREATE TABLE metrics_poll_status (
    host_id         TEXT PRIMARY KEY REFERENCES core_hosts (id) ON DELETE CASCADE,
    last_ok         TEXT,
    last_error_code TEXT,
    last_error_at   TEXT,
    clock_skew_s    INTEGER,
    cpus            INTEGER -- the agent's logical CPUs at its last answer, for load rules
) STRICT;

-- Alert rules (spec section 5.3). threshold is a percent for cpu, memory,
-- swap and disk, load per logical CPU for load; an offline rule stores '>'
-- and 0 and fires on the fleet session state. target is '*' or a host
-- group id.
CREATE TABLE metrics_alert_rules (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE COLLATE NOCASE,
    metric       TEXT NOT NULL CHECK (metric IN ('cpu', 'memory', 'swap', 'disk', 'load', 'offline')),
    comparator   TEXT NOT NULL CHECK (comparator IN ('>', '<')),
    threshold    REAL NOT NULL,
    duration_min INTEGER NOT NULL CHECK (duration_min BETWEEN 1 AND 1440),
    severity     TEXT NOT NULL CHECK (severity IN ('warning', 'critical')),
    target       TEXT NOT NULL,
    enabled      INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
) STRICT;

-- Each alert's state: subject is the mount of a disk rule, '' otherwise;
-- since is when it last fired or resolved; value the figure at that moment.
CREATE TABLE metrics_alerts (
    rule_id TEXT NOT NULL REFERENCES metrics_alert_rules (id) ON DELETE CASCADE,
    host_id TEXT NOT NULL REFERENCES core_hosts (id) ON DELETE CASCADE,
    subject TEXT NOT NULL,
    state   TEXT NOT NULL CHECK (state IN ('firing', 'resolved')),
    since   TEXT NOT NULL,
    value   REAL NOT NULL,
    PRIMARY KEY (rule_id, host_id, subject)
) STRICT;

CREATE INDEX metrics_alerts_since ON metrics_alerts (state, since);

-- The default rules, seeded here rather than at start so a deleted one
-- stays deleted.
INSERT INTO metrics_alert_rules (id, name, metric, comparator, threshold, duration_min, severity, target, enabled, created_at, updated_at)
VALUES ('host-offline', 'Host offline', 'offline', '>', 0, 5, 'critical', '*', 1, '2026-11-24T00:00:00Z', '2026-11-24T00:00:00Z'),
       ('disk-almost-full', 'Disk almost full', 'disk', '>', 90, 10, 'warning', '*', 1, '2026-11-24T00:00:00Z', '2026-11-24T00:00:00Z');

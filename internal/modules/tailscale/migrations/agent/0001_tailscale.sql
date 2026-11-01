-- One row per tailscale.package.pin change: its agent-local transaction (spec 4.1).
CREATE TABLE tailscale_pins (
    change_id      TEXT PRIMARY KEY,
    version        TEXT NOT NULL,
    arch           TEXT NOT NULL,
    prior_version  TEXT NOT NULL,
    prior_held     INTEGER NOT NULL CHECK (prior_held IN (0, 1)),
    prior_backend  TEXT NOT NULL,
    link_tailnet   INTEGER NOT NULL CHECK (link_tailnet IN (0, 1)),
    phase          TEXT NOT NULL CHECK (phase IN ('recorded', 'unheld', 'staged', 'installed', 'held', 'checking', 'done', 'rolling_back', 'rolled_back', 'failed')),
    staged_deb     TEXT NOT NULL DEFAULT '',
    staged_sha256  TEXT NOT NULL DEFAULT '',
    unpinned_by    TEXT NOT NULL DEFAULT '',
    updated_at     TEXT NOT NULL
) STRICT;
-- Every step a pin or its rollback took, in order.
CREATE TABLE tailscale_pin_steps (
    change_id  TEXT NOT NULL REFERENCES tailscale_pins (change_id),
    seq        INTEGER NOT NULL,
    step       TEXT NOT NULL,
    result     TEXT NOT NULL,
    at         TEXT NOT NULL,
    PRIMARY KEY (change_id, seq)
) STRICT;
-- The package installs Darkpanel made, so uninstall removes only a package it installed.
CREATE TABLE tailscale_installs (
    change_id    TEXT PRIMARY KEY,
    version      TEXT NOT NULL,
    installed_at TEXT NOT NULL,
    removed_at   TEXT NOT NULL DEFAULT ''
) STRICT;

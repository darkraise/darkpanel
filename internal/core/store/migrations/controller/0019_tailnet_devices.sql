-- Remembered tailnet devices: Tailscale "Continue as" signs a node in without
-- a code only after its user typed their TOTP code once from that node.

CREATE TABLE core_user_tailnet_devices (
    user_id       TEXT NOT NULL REFERENCES core_users(id) ON DELETE CASCADE,
    stable_id     TEXT NOT NULL,
    node_name     TEXT NOT NULL,
    remembered_at TEXT NOT NULL,
    last_used_at  TEXT NOT NULL,
    PRIMARY KEY (user_id, stable_id)
) STRICT;

-- Earlier rows hold addresses and stable ids a host reported about itself;
-- from now on only the address the controller saw a session come from is
-- recorded. stable_id stays, written as '', because the migration runner
-- promises an older binary runs on a newer schema (internal/core/store/migrate.go),
-- and that binary still writes the column.
DELETE FROM core_host_tailnet;

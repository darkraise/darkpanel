-- Sign-in methods (M10 spec sections 2 and 3): the identities OIDC and
-- Tailscale sign-in match users by, the one row of method settings, and the
-- tailnet addresses of the hosts, which may never sign in as their owner.
-- Existing rows keep every value: the new columns start null.

ALTER TABLE core_users ADD COLUMN email TEXT;
ALTER TABLE core_users ADD COLUMN oidc_issuer TEXT;
ALTER TABLE core_users ADD COLUMN oidc_subject TEXT;
ALTER TABLE core_users ADD COLUMN tailnet_login TEXT;
ALTER TABLE core_users ADD COLUMN tailnet_user_id TEXT;
ALTER TABLE core_users ADD COLUMN disabled_at TEXT;

CREATE UNIQUE INDEX core_users_email ON core_users (email COLLATE NOCASE) WHERE email IS NOT NULL;
CREATE UNIQUE INDEX core_users_tailnet_login ON core_users (tailnet_login COLLATE NOCASE) WHERE tailnet_login IS NOT NULL;
CREATE UNIQUE INDEX core_users_oidc ON core_users (oidc_issuer, oidc_subject)
    WHERE oidc_issuer IS NOT NULL AND oidc_subject IS NOT NULL;

-- One row. OIDC is configured while oidc_issuer is set; the client secret is
-- sealed with associated data "auth_settings.oidc_client_secret_ct.1".
CREATE TABLE auth_settings (
    id                     INTEGER PRIMARY KEY CHECK (id = 1),
    oidc_issuer            TEXT,
    oidc_client_id         TEXT,
    oidc_client_secret_ct  BLOB,
    oidc_display_name      TEXT,
    oidc_redirect_base     TEXT,
    oidc_require_totp      INTEGER NOT NULL DEFAULT 0 CHECK (oidc_require_totp IN (0, 1)),
    tailscale_enabled      INTEGER NOT NULL DEFAULT 0 CHECK (tailscale_enabled IN (0, 1)),
    tailscale_require_totp INTEGER NOT NULL DEFAULT 0 CHECK (tailscale_require_totp IN (0, 1)),
    updated_at             TEXT NOT NULL
) STRICT;

-- Each host's tailnet addresses and node id, written at every agent connect.
CREATE TABLE core_host_tailnet (
    host_id   TEXT NOT NULL REFERENCES core_hosts (id) ON DELETE CASCADE,
    addr      TEXT NOT NULL,
    stable_id TEXT NOT NULL,
    seen_at   TEXT NOT NULL,
    PRIMARY KEY (host_id, addr)
) STRICT;

CREATE INDEX core_host_tailnet_addr ON core_host_tailnet (addr);

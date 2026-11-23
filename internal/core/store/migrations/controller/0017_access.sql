-- Host groups, member grants and enrollment links (M10 spec sections 1.1 and
-- 1.5), read and written only through internal/controller/auth and the core
-- user and group actions. No existing row changes: every user stays admin.

CREATE TABLE core_host_groups (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
    created_at TEXT NOT NULL
) STRICT;

CREATE TABLE core_host_group_members (
    group_id TEXT NOT NULL REFERENCES core_host_groups (id) ON DELETE CASCADE,
    host_id  TEXT NOT NULL REFERENCES core_hosts (id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, host_id)
) STRICT;

CREATE INDEX core_host_group_members_host ON core_host_group_members (host_id);

-- target is a core_host_groups id or the literal '*' (every host, including
-- hosts enrolled later), so it carries no foreign key: core.group.delete
-- refuses while a grant targets the group.
CREATE TABLE core_user_grants (
    user_id TEXT NOT NULL REFERENCES core_users (id) ON DELETE CASCADE,
    target  TEXT NOT NULL,
    role    TEXT NOT NULL CHECK (role IN ('viewer', 'operator')),
    PRIMARY KEY (user_id, target)
) STRICT;

CREATE INDEX core_user_grants_target ON core_user_grants (target);

-- The enrollment token is stored hashed and is single-use: used_at is set
-- when enrollment finishes.
CREATE TABLE core_user_enrollments (
    user_id    TEXT PRIMARY KEY REFERENCES core_users (id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    used_at    TEXT
) STRICT;

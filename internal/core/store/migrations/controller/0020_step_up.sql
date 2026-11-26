-- Step-up (M11 spec section 1.1): until when a session's fresh TOTP code
-- lets it run step-up actions. Existing sessions start null, not elevated.
-- The last accepted TOTP step is core_users.totp_last_step, which sign-in
-- already keeps.

ALTER TABLE core_sessions ADD COLUMN elevated_until TEXT;

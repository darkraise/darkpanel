-- The job timers the module stopped when it was turned off, which it starts
-- again when it is turned on (M11 spec section 6).
CREATE TABLE jobs_paused (
    timer TEXT PRIMARY KEY
) STRICT;

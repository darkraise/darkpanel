-- The open terminals (M11 spec section 5): a row lives from a terminal's
-- open entry to its close entry, so an agent that starts after a crash
-- journals the close of what was left open.
CREATE TABLE console_terminals (
    id TEXT PRIMARY KEY,
    actor TEXT NOT NULL,
    source TEXT NOT NULL,
    opened_at TEXT NOT NULL
) STRICT;

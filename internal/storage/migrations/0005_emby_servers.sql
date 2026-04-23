CREATE TABLE IF NOT EXISTS emby_servers (
    key TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    upstream_url TEXT NOT NULL,
    api_key TEXT,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_emby_servers_enabled ON emby_servers(enabled);

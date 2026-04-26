CREATE TABLE IF NOT EXISTS providers (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    root_path TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'unknown',
    last_check_at TEXT,
    last_error TEXT,
    config_json TEXT,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_providers_type ON providers(type);
CREATE INDEX IF NOT EXISTS idx_providers_enabled ON providers(enabled);

CREATE TABLE IF NOT EXISTS provider_secrets (
    provider_id TEXT NOT NULL,
    secret_type TEXT NOT NULL,
    secret_value TEXT NOT NULL,
    masked_value TEXT,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (provider_id, secret_type),
    FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_provider_secrets_provider_id ON provider_secrets(provider_id);

CREATE TABLE IF NOT EXISTS libraries (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    last_scan_at TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_libraries_enabled ON libraries(enabled);

CREATE TABLE IF NOT EXISTS library_mounts (
    id TEXT PRIMARY KEY,
    library_id TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    source_path TEXT NOT NULL,
    target_path TEXT NOT NULL,
    media_type TEXT,
    priority INTEGER NOT NULL DEFAULT 100,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (library_id) REFERENCES libraries(id) ON DELETE CASCADE,
    FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_library_mounts_library_id ON library_mounts(library_id);
CREATE INDEX IF NOT EXISTS idx_library_mounts_provider_id ON library_mounts(provider_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_library_mounts_unique ON library_mounts(library_id, provider_id, source_path, target_path);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value_json TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS admin_users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    last_login_at TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS entries (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    entry_type TEXT NOT NULL,
    path TEXT NOT NULL,
    parent_path TEXT,
    name TEXT NOT NULL,
    size BIGINT,
    mtime TEXT,
    mime_type TEXT,
    content_hash TEXT,
    last_seen_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_entries_provider_path ON entries(provider_id, path);
CREATE INDEX IF NOT EXISTS idx_entries_provider_parent ON entries(provider_id, parent_path);
CREATE INDEX IF NOT EXISTS idx_entries_provider_name ON entries(provider_id, name);
CREATE INDEX IF NOT EXISTS idx_entries_last_seen_at ON entries(last_seen_at);

CREATE TABLE IF NOT EXISTS direct_link_cache (
    provider_id TEXT NOT NULL,
    path TEXT NOT NULL,
    url TEXT NOT NULL,
    headers_json TEXT,
    supports_range INTEGER NOT NULL DEFAULT 0 CHECK (supports_range IN (0, 1)),
    expire_at TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (provider_id, path),
    FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_direct_link_cache_expire_at ON direct_link_cache(expire_at);

CREATE TABLE IF NOT EXISTS scan_tasks (
    id TEXT PRIMARY KEY,
    task_type TEXT NOT NULL,
    library_id TEXT,
    status TEXT NOT NULL,
    progress_total INTEGER,
    progress_done INTEGER,
    message TEXT,
    error_message TEXT,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (library_id) REFERENCES libraries(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_scan_tasks_status ON scan_tasks(status);
CREATE INDEX IF NOT EXISTS idx_scan_tasks_library_id ON scan_tasks(library_id);
CREATE INDEX IF NOT EXISTS idx_scan_tasks_started_at ON scan_tasks(started_at);

CREATE TABLE IF NOT EXISTS playback_logs (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    path TEXT NOT NULL,
    mode TEXT NOT NULL,
    client TEXT,
    user_agent TEXT,
    status_code INTEGER NOT NULL,
    duration_ms INTEGER,
    remote_addr TEXT,
    error_message TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_playback_logs_provider_id ON playback_logs(provider_id);
CREATE INDEX IF NOT EXISTS idx_playback_logs_created_at ON playback_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_playback_logs_status_code ON playback_logs(status_code);

CREATE TABLE IF NOT EXISTS system_events (
    id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    level TEXT NOT NULL,
    source TEXT NOT NULL,
    message TEXT NOT NULL,
    payload_json TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_system_events_type ON system_events(event_type);
CREATE INDEX IF NOT EXISTS idx_system_events_level ON system_events(level);
CREATE INDEX IF NOT EXISTS idx_system_events_created_at ON system_events(created_at);

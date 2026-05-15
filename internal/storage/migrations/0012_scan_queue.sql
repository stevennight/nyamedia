CREATE TABLE IF NOT EXISTS scan_queue (
    id TEXT PRIMARY KEY,
    library_id TEXT NOT NULL,
    mount_id TEXT,
    provider_id TEXT NOT NULL,
    source_path TEXT NOT NULL,
    mode TEXT NOT NULL CHECK (mode IN ('current_level', 'recursive')),
    source TEXT NOT NULL,
    run_after TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'claimed')),
    event_count INTEGER NOT NULL DEFAULT 1,
    last_event_at TEXT NOT NULL,
    options_json TEXT,
    reason_json TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (library_id) REFERENCES libraries(id) ON DELETE CASCADE,
    FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_scan_queue_merge_key
ON scan_queue(library_id, COALESCE(mount_id, ''), provider_id, source_path, mode);

CREATE INDEX IF NOT EXISTS idx_scan_queue_pending_run_after
ON scan_queue(status, run_after, created_at, id);

CREATE INDEX IF NOT EXISTS idx_scan_queue_provider_status
ON scan_queue(provider_id, status);

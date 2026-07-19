CREATE TABLE IF NOT EXISTS scan_schedules (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    library_id TEXT NOT NULL,
    mount_id TEXT,
    source_path TEXT,
    cron TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    last_run_at TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (library_id) REFERENCES libraries(id) ON DELETE CASCADE,
    FOREIGN KEY (mount_id) REFERENCES library_mounts(id) ON DELETE CASCADE,
    CHECK (mount_id IS NOT NULL OR source_path IS NULL)
);

CREATE INDEX IF NOT EXISTS idx_scan_schedules_enabled
ON scan_schedules(enabled);

CREATE INDEX IF NOT EXISTS idx_scan_schedules_library_id
ON scan_schedules(library_id);

CREATE INDEX IF NOT EXISTS idx_scan_schedules_mount_id
ON scan_schedules(mount_id);

INSERT INTO scan_schedules (id, name, library_id, cron, enabled)
SELECT 'legacy-library-scan-' || id,
       name || ' scheduled scan',
       id,
       TRIM(scan_cron),
       enabled
FROM libraries
WHERE TRIM(COALESCE(scan_cron, '')) <> ''
ON CONFLICT (id) DO NOTHING;

UPDATE libraries
SET scan_cron = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE TRIM(COALESCE(scan_cron, '')) <> '';

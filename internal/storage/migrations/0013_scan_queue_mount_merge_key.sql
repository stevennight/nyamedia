DROP INDEX IF EXISTS uq_scan_queue_merge_key;

CREATE UNIQUE INDEX IF NOT EXISTS uq_scan_queue_merge_key
ON scan_queue(library_id, COALESCE(mount_id, ''), provider_id, source_path, mode);

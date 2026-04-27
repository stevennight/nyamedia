CREATE INDEX IF NOT EXISTS idx_scan_tasks_active_type_created_id
ON scan_tasks(task_type, created_at DESC, id DESC)
WHERE status IN ('pending', 'running');

CREATE INDEX IF NOT EXISTS idx_scan_tasks_active_type_library_created_id
ON scan_tasks(task_type, library_id, created_at DESC, id DESC)
WHERE status IN ('pending', 'running');

CREATE INDEX IF NOT EXISTS idx_scan_tasks_finished_prune_cutoff
ON scan_tasks((COALESCE(NULLIF(finished_at, ''), updated_at, created_at)))
WHERE status NOT IN ('pending', 'running');

CREATE INDEX IF NOT EXISTS idx_entries_updated_provider_path ON entries(updated_at DESC, provider_id, path);

CREATE INDEX IF NOT EXISTS idx_entries_provider_updated_path ON entries(provider_id, updated_at DESC, path);

CREATE INDEX IF NOT EXISTS idx_task_logs_task_created_id ON task_logs(task_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_system_events_created_id ON system_events(created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_scan_tasks_created_id ON scan_tasks(created_at DESC, id DESC);

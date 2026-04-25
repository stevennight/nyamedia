ALTER TABLE entries ADD COLUMN provider_entry_id TEXT;

ALTER TABLE entries ADD COLUMN metadata_json TEXT;

CREATE INDEX IF NOT EXISTS idx_entries_provider_entry_id ON entries(provider_id, provider_entry_id);

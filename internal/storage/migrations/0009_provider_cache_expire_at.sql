ALTER TABLE provider_cache ADD COLUMN expire_at TEXT;

CREATE INDEX IF NOT EXISTS idx_provider_cache_expire_at ON provider_cache(expire_at);

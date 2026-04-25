CREATE TABLE IF NOT EXISTS provider_cache (
    provider_id TEXT NOT NULL,
    cache_key TEXT NOT NULL,
    cache_value TEXT NOT NULL,
    expire_at TEXT,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (provider_id, cache_key),
    FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_provider_cache_provider_id ON provider_cache(provider_id);
CREATE INDEX IF NOT EXISTS idx_provider_cache_expire_at ON provider_cache(expire_at);

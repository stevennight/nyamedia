ALTER TABLE providers ADD COLUMN watch_enabled INTEGER NOT NULL DEFAULT 1 CHECK (watch_enabled IN (0, 1));

CREATE INDEX IF NOT EXISTS idx_providers_watch_enabled ON providers(watch_enabled);

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type ProviderCacheRepository struct {
	db *sql.DB
}

func NewProviderCacheRepository(db *sql.DB) *ProviderCacheRepository {
	return &ProviderCacheRepository{db: db}
}

func (r *ProviderCacheRepository) Get(ctx context.Context, providerID, key string) (string, bool, error) {
	var value string
	err := r.db.QueryRowContext(ctx, `
SELECT cache_value
FROM provider_cache
WHERE provider_id = ?
  AND cache_key = ?
  AND (expire_at IS NULL OR expire_at > CURRENT_TIMESTAMP)`, providerID, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get provider cache %s/%s: %w", providerID, key, err)
	}
	return value, true, nil
}

func (r *ProviderCacheRepository) Set(ctx context.Context, providerID, key, value string) error {
	return r.SetWithTTL(ctx, providerID, key, value, 0)
}

func (r *ProviderCacheRepository) SetWithTTL(ctx context.Context, providerID, key, value string, ttl time.Duration) error {
	var expireAt any
	if ttl > 0 {
		expireAt = time.Now().UTC().Add(ttl).Format(time.RFC3339)
	}
	const query = `
INSERT INTO provider_cache (provider_id, cache_key, cache_value, expire_at, updated_at)
VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(provider_id, cache_key) DO UPDATE SET
    cache_value = excluded.cache_value,
    expire_at = excluded.expire_at,
    updated_at = CURRENT_TIMESTAMP`
	if _, err := r.db.ExecContext(ctx, query, providerID, key, value, expireAt); err != nil {
		return fmt.Errorf("set provider cache %s/%s: %w", providerID, key, err)
	}
	return nil
}

func (r *ProviderCacheRepository) DeleteExpired(ctx context.Context) (int64, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM provider_cache WHERE expire_at IS NOT NULL AND expire_at <= CURRENT_TIMESTAMP`)
	if err != nil {
		return 0, fmt.Errorf("delete expired provider cache: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read expired provider cache delete count: %w", err)
	}
	return deleted, nil
}

func (r *ProviderCacheRepository) DeleteProvider(ctx context.Context, providerID string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM provider_cache WHERE provider_id = ?`, providerID); err != nil {
		return fmt.Errorf("delete provider cache %s: %w", providerID, err)
	}
	return nil
}

func (r *ProviderCacheRepository) Delete(ctx context.Context, providerID, key string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM provider_cache WHERE provider_id = ? AND cache_key = ?`, providerID, key); err != nil {
		return fmt.Errorf("delete provider cache %s/%s: %w", providerID, key, err)
	}
	return nil
}

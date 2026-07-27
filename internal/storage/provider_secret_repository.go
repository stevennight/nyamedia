package storage

import (
	"context"
	"database/sql"
	"fmt"

	"NyaMedia/internal/model"
)

type ProviderSecretRepository struct {
	db *sql.DB
}

func NewProviderSecretRepository(db *sql.DB) *ProviderSecretRepository {
	return &ProviderSecretRepository{db: db}
}

func (r *ProviderSecretRepository) ListByProvider(ctx context.Context, providerID string) ([]model.ProviderSecret, error) {
	const query = `
SELECT provider_id, secret_type, secret_value, COALESCE(masked_value, ''), updated_at
FROM provider_secrets
WHERE provider_id = ?
ORDER BY secret_type`

	rows, err := r.db.QueryContext(ctx, query, providerID)
	if err != nil {
		return nil, fmt.Errorf("list provider secrets for %s: %w", providerID, err)
	}
	defer rows.Close()

	items := make([]model.ProviderSecret, 0)
	for rows.Next() {
		var item model.ProviderSecret
		if err := rows.Scan(&item.ProviderID, &item.SecretType, &item.SecretValue, &item.MaskedValue, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan provider secret: %w", err)
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provider secrets for %s: %w", providerID, err)
	}
	return items, nil
}

func (r *ProviderSecretRepository) Get(ctx context.Context, providerID, secretType string) (*model.ProviderSecret, error) {
	const query = `
SELECT provider_id, secret_type, secret_value, COALESCE(masked_value, ''), updated_at
FROM provider_secrets
WHERE provider_id = ? AND secret_type = ?`

	var item model.ProviderSecret
	err := r.db.QueryRowContext(ctx, query, providerID, secretType).
		Scan(&item.ProviderID, &item.SecretType, &item.SecretValue, &item.MaskedValue, &item.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get provider secret %s/%s: %w", providerID, secretType, err)
	}
	return &item, nil
}

func (r *ProviderSecretRepository) Upsert(ctx context.Context, item model.ProviderSecret) error {
	const query = `
INSERT INTO provider_secrets (provider_id, secret_type, secret_value, masked_value)
VALUES (?, ?, ?, NULLIF(?, ''))
ON CONFLICT(provider_id, secret_type) DO UPDATE SET
    secret_value = excluded.secret_value,
    masked_value = excluded.masked_value,
    updated_at = CURRENT_TIMESTAMP`

	_, err := r.db.ExecContext(ctx, query, item.ProviderID, item.SecretType, item.SecretValue, item.MaskedValue)
	if err != nil {
		return fmt.Errorf("upsert provider secret %s/%s: %w", item.ProviderID, item.SecretType, err)
	}
	return nil
}

func (r *ProviderSecretRepository) UpsertMany(ctx context.Context, items []model.ProviderSecret) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin provider secret batch: %w", err)
	}
	defer tx.Rollback()

	const query = `
INSERT INTO provider_secrets (provider_id, secret_type, secret_value, masked_value)
VALUES (?, ?, ?, NULLIF(?, ''))
ON CONFLICT(provider_id, secret_type) DO UPDATE SET
    secret_value = excluded.secret_value,
    masked_value = excluded.masked_value,
    updated_at = CURRENT_TIMESTAMP`
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, query, item.ProviderID, item.SecretType, item.SecretValue, item.MaskedValue); err != nil {
			return fmt.Errorf("upsert provider secret %s/%s: %w", item.ProviderID, item.SecretType, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit provider secret batch: %w", err)
	}
	return nil
}

func (r *ProviderSecretRepository) ReplaceMany(
	ctx context.Context,
	providerID string,
	items []model.ProviderSecret,
	deleteTypes []string,
) error {
	return r.replaceMany(ctx, providerID, items, deleteTypes, false)
}

func (r *ProviderSecretRepository) ReplaceManyAndInvalidateProviderState(
	ctx context.Context,
	providerID string,
	items []model.ProviderSecret,
	deleteTypes []string,
) error {
	return r.replaceMany(ctx, providerID, items, deleteTypes, true)
}

func (r *ProviderSecretRepository) replaceMany(
	ctx context.Context,
	providerID string,
	items []model.ProviderSecret,
	deleteTypes []string,
	invalidateProviderState bool,
) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin provider secret replacement: %w", err)
	}
	defer tx.Rollback()

	for _, secretType := range deleteTypes {
		if _, err := tx.ExecContext(ctx, `DELETE FROM provider_secrets WHERE provider_id = ? AND secret_type = ?`, providerID, secretType); err != nil {
			return fmt.Errorf("delete provider secret %s/%s: %w", providerID, secretType, err)
		}
	}

	const query = `
INSERT INTO provider_secrets (provider_id, secret_type, secret_value, masked_value)
VALUES (?, ?, ?, NULLIF(?, ''))
ON CONFLICT(provider_id, secret_type) DO UPDATE SET
    secret_value = excluded.secret_value,
    masked_value = excluded.masked_value,
    updated_at = CURRENT_TIMESTAMP`
	for _, item := range items {
		if item.ProviderID != providerID {
			return fmt.Errorf("provider secret %s/%s does not belong to %s", item.ProviderID, item.SecretType, providerID)
		}
		if _, err := tx.ExecContext(ctx, query, item.ProviderID, item.SecretType, item.SecretValue, item.MaskedValue); err != nil {
			return fmt.Errorf("upsert provider secret %s/%s: %w", item.ProviderID, item.SecretType, err)
		}
	}
	if invalidateProviderState {
		for _, query := range []struct {
			name string
			sql  string
		}{
			{name: "provider_cache", sql: `DELETE FROM provider_cache WHERE provider_id = ?`},
			{name: "direct_link_cache", sql: `DELETE FROM direct_link_cache WHERE provider_id = ?`},
			{name: "entries", sql: `DELETE FROM entries WHERE provider_id = ?`},
		} {
			if _, err := tx.ExecContext(ctx, query.sql, providerID); err != nil {
				return fmt.Errorf("invalidate %s for provider %s: %w", query.name, providerID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit provider secret replacement: %w", err)
	}
	return nil
}

func (r *ProviderSecretRepository) Delete(ctx context.Context, providerID, secretType string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM provider_secrets WHERE provider_id = ? AND secret_type = ?`, providerID, secretType)
	if err != nil {
		return fmt.Errorf("delete provider secret %s/%s: %w", providerID, secretType, err)
	}
	return ensureRowsAffected(result, "provider secret not found")
}

package storage

import (
	"context"
	"database/sql"
	"fmt"

	"emby115/internal/model"
)

type ProviderRepository struct {
	db *sql.DB
}

func NewProviderRepository(db *sql.DB) *ProviderRepository {
	return &ProviderRepository{db: db}
}

func (r *ProviderRepository) List(ctx context.Context) ([]model.Provider, error) {
	const query = `
SELECT id, type, name, root_path, status, COALESCE(last_check_at, ''), COALESCE(last_error, ''),
	       COALESCE(config_json, ''), enabled, watch_enabled, created_at, updated_at
FROM providers
ORDER BY id`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()

	providers := make([]model.Provider, 0)
	for rows.Next() {
		provider, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate providers: %w", err)
	}

	return providers, nil
}

func (r *ProviderRepository) Get(ctx context.Context, id string) (*model.Provider, error) {
	const query = `
SELECT id, type, name, root_path, status, COALESCE(last_check_at, ''), COALESCE(last_error, ''),
	       COALESCE(config_json, ''), enabled, watch_enabled, created_at, updated_at
FROM providers
WHERE id = ?`

	provider, err := scanProviderRow(r.db.QueryRowContext(ctx, query, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get provider %s: %w", id, err)
	}

	return provider, nil
}

func (r *ProviderRepository) Create(ctx context.Context, provider model.Provider) error {
	const query = `
INSERT INTO providers (id, type, name, root_path, status, last_check_at, last_error, config_json, enabled, watch_enabled)
VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?)`

	_, err := r.db.ExecContext(ctx, query,
		provider.ID,
		provider.Type,
		provider.Name,
		provider.RootPath,
		providerStatusOrDefault(provider.Status),
		provider.LastCheckAt,
		provider.LastError,
		provider.ConfigJSON,
		boolToInt(provider.Enabled),
		boolToInt(provider.WatchEnabled),
	)
	if err != nil {
		return fmt.Errorf("create provider %s: %w", provider.ID, err)
	}
	return nil
}

func (r *ProviderRepository) Update(ctx context.Context, provider model.Provider) error {
	const query = `
UPDATE providers
SET type = ?,
    name = ?,
    root_path = ?,
    status = ?,
    last_check_at = NULLIF(?, ''),
    last_error = NULLIF(?, ''),
    config_json = NULLIF(?, ''),
    enabled = ?,
    watch_enabled = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?`

	result, err := r.db.ExecContext(ctx, query,
		provider.Type,
		provider.Name,
		provider.RootPath,
		providerStatusOrDefault(provider.Status),
		provider.LastCheckAt,
		provider.LastError,
		provider.ConfigJSON,
		boolToInt(provider.Enabled),
		boolToInt(provider.WatchEnabled),
		provider.ID,
	)
	if err != nil {
		return fmt.Errorf("update provider %s: %w", provider.ID, err)
	}

	return ensureRowsAffected(result, "provider not found")
}

func (r *ProviderRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM providers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete provider %s: %w", id, err)
	}
	return ensureRowsAffected(result, "provider not found")
}

func scanProvider(scanner interface{ Scan(dest ...any) error }) (model.Provider, error) {
	providerPtr, err := scanProviderRow(scanner)
	if err != nil {
		return model.Provider{}, err
	}
	return *providerPtr, nil
}

func scanProviderRow(scanner interface{ Scan(dest ...any) error }) (*model.Provider, error) {
	var provider model.Provider
	var enabled int
	var watchEnabled int
	err := scanner.Scan(
		&provider.ID,
		&provider.Type,
		&provider.Name,
		&provider.RootPath,
		&provider.Status,
		&provider.LastCheckAt,
		&provider.LastError,
		&provider.ConfigJSON,
		&enabled,
		&watchEnabled,
		&provider.CreatedAt,
		&provider.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	provider.Enabled = enabled == 1
	provider.WatchEnabled = watchEnabled == 1
	return &provider, nil
}

func providerStatusOrDefault(status model.ProviderStatus) model.ProviderStatus {
	if status == "" {
		return model.ProviderStatusUnknown
	}
	return status
}

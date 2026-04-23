package storage

import (
	"context"
	"database/sql"
	"fmt"

	"emby115/internal/model"
)

type EmbyServerRepository struct {
	db *sql.DB
}

func NewEmbyServerRepository(db *sql.DB) *EmbyServerRepository {
	return &EmbyServerRepository{db: db}
}

func (r *EmbyServerRepository) List(ctx context.Context) ([]model.EmbyServer, error) {
	const query = `
SELECT key, name, upstream_url, COALESCE(api_key, ''), enabled, created_at, updated_at
FROM emby_servers
ORDER BY key`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list emby servers: %w", err)
	}
	defer rows.Close()

	items := make([]model.EmbyServer, 0)
	for rows.Next() {
		item, err := scanEmbyServer(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate emby servers: %w", err)
	}
	return items, nil
}

func (r *EmbyServerRepository) Get(ctx context.Context, key string) (*model.EmbyServer, error) {
	const query = `
SELECT key, name, upstream_url, COALESCE(api_key, ''), enabled, created_at, updated_at
FROM emby_servers
WHERE key = ?`

	item, err := scanEmbyServerRow(r.db.QueryRowContext(ctx, query, key))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get emby server %s: %w", key, err)
	}
	return item, nil
}

func (r *EmbyServerRepository) GetByName(ctx context.Context, name string) (*model.EmbyServer, error) {
	const query = `
SELECT key, name, upstream_url, COALESCE(api_key, ''), enabled, created_at, updated_at
FROM emby_servers
WHERE name = ?`

	item, err := scanEmbyServerRow(r.db.QueryRowContext(ctx, query, name))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get emby server by name %s: %w", name, err)
	}
	return item, nil
}

func (r *EmbyServerRepository) Create(ctx context.Context, item model.EmbyServer) error {
	const query = `
INSERT INTO emby_servers (key, name, upstream_url, api_key, enabled)
VALUES (?, ?, ?, NULLIF(?, ''), ?)`

	_, err := r.db.ExecContext(ctx, query, item.Key, item.Name, item.UpstreamURL, item.APIKey, boolToInt(item.Enabled))
	if err != nil {
		return fmt.Errorf("create emby server %s: %w", item.Key, err)
	}
	return nil
}

func (r *EmbyServerRepository) Update(ctx context.Context, item model.EmbyServer) error {
	const query = `
UPDATE emby_servers
SET name = ?,
    upstream_url = ?,
    api_key = NULLIF(?, ''),
    enabled = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE key = ?`

	result, err := r.db.ExecContext(ctx, query, item.Name, item.UpstreamURL, item.APIKey, boolToInt(item.Enabled), item.Key)
	if err != nil {
		return fmt.Errorf("update emby server %s: %w", item.Key, err)
	}
	return ensureRowsAffected(result, "emby server not found")
}

func (r *EmbyServerRepository) Delete(ctx context.Context, key string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM emby_servers WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("delete emby server %s: %w", key, err)
	}
	return ensureRowsAffected(result, "emby server not found")
}

func scanEmbyServer(scanner interface{ Scan(dest ...any) error }) (model.EmbyServer, error) {
	itemPtr, err := scanEmbyServerRow(scanner)
	if err != nil {
		return model.EmbyServer{}, err
	}
	return *itemPtr, nil
}

func scanEmbyServerRow(scanner interface{ Scan(dest ...any) error }) (*model.EmbyServer, error) {
	var item model.EmbyServer
	var enabled int
	err := scanner.Scan(
		&item.Key,
		&item.Name,
		&item.UpstreamURL,
		&item.APIKey,
		&enabled,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	item.Enabled = enabled == 1
	return &item, nil
}

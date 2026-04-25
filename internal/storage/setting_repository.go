package storage

import (
	"context"
	"database/sql"
	"fmt"

	"NyaMedia/internal/model"
)

type SettingRepository struct {
	db *sql.DB
}

func NewSettingRepository(db *sql.DB) *SettingRepository {
	return &SettingRepository{db: db}
}

func (r *SettingRepository) List(ctx context.Context) ([]model.Setting, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT key, value_json, updated_at FROM settings ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	defer rows.Close()

	items := make([]model.Setting, 0)
	for rows.Next() {
		var item model.Setting
		if err := rows.Scan(&item.Key, &item.ValueJSON, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate settings: %w", err)
	}
	return items, nil
}

func (r *SettingRepository) Get(ctx context.Context, key string) (*model.Setting, error) {
	var item model.Setting
	err := r.db.QueryRowContext(ctx, `SELECT key, value_json, updated_at FROM settings WHERE key = ?`, key).
		Scan(&item.Key, &item.ValueJSON, &item.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get setting %s: %w", key, err)
	}
	return &item, nil
}

func (r *SettingRepository) Upsert(ctx context.Context, item model.Setting) error {
	const query = `
INSERT INTO settings (key, value_json)
VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json, updated_at = CURRENT_TIMESTAMP`

	_, err := r.db.ExecContext(ctx, query, item.Key, item.ValueJSON)
	if err != nil {
		return fmt.Errorf("upsert setting %s: %w", item.Key, err)
	}
	return nil
}

func (r *SettingRepository) Delete(ctx context.Context, key string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("delete setting %s: %w", key, err)
	}
	return ensureRowsAffected(result, "setting not found")
}

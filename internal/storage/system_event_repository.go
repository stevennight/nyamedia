package storage

import (
	"context"
	"database/sql"
	"fmt"

	"emby115/internal/model"
)

type SystemEventRepository struct {
	db *sql.DB
}

func NewSystemEventRepository(db *sql.DB) *SystemEventRepository {
	return &SystemEventRepository{db: db}
}

func (r *SystemEventRepository) Create(ctx context.Context, item model.SystemEvent) error {
	const query = `
INSERT INTO system_events (id, event_type, level, source, message, payload_json)
VALUES (?, ?, ?, ?, ?, NULLIF(?, ''))`

	_, err := r.db.ExecContext(ctx, query, item.ID, item.EventType, item.Level, item.Source, item.Message, item.PayloadJSON)
	if err != nil {
		return fmt.Errorf("create system event %s: %w", item.ID, err)
	}
	return nil
}

func (r *SystemEventRepository) List(ctx context.Context, limit int) ([]model.SystemEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	const query = `
SELECT id, event_type, level, source, message, COALESCE(payload_json, ''), created_at
FROM system_events
ORDER BY created_at DESC, id DESC
LIMIT ?`

	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list system events: %w", err)
	}
	defer rows.Close()

	items := make([]model.SystemEvent, 0)
	for rows.Next() {
		var item model.SystemEvent
		if err := rows.Scan(&item.ID, &item.EventType, &item.Level, &item.Source, &item.Message, &item.PayloadJSON, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan system event: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate system events: %w", err)
	}
	return items, nil
}

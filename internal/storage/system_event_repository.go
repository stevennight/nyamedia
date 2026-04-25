package storage

import (
	"context"
	"database/sql"
	"fmt"

	"NyaMedia/internal/model"
)

type SystemEventRepository struct {
	db *sql.DB
}

type SystemEventListOptions struct {
	Limit           int
	Source          string
	EventType       string
	BeforeCreatedAt string
	BeforeID        string
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

func (r *SystemEventRepository) List(ctx context.Context, options SystemEventListOptions) ([]model.SystemEvent, bool, error) {
	limit := options.Limit
	if limit <= 0 {
		limit = 100
	}
	query := `
SELECT id, event_type, level, source, message, COALESCE(payload_json, ''), created_at
FROM system_events
WHERE 1 = 1`
	args := []any{}
	if options.Source != "" {
		query += `
  AND source = ?`
		args = append(args, options.Source)
	}
	if options.EventType != "" {
		query += `
  AND event_type = ?`
		args = append(args, options.EventType)
	}
	if options.BeforeCreatedAt != "" && options.BeforeID != "" {
		query += `
  AND (created_at < ? OR (created_at = ? AND id < ?))`
		args = append(args, options.BeforeCreatedAt, options.BeforeCreatedAt, options.BeforeID)
	}
	query += `
ORDER BY created_at DESC, id DESC
LIMIT ?`
	args = append(args, limit+1)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("list system events: %w", err)
	}
	defer rows.Close()

	items := make([]model.SystemEvent, 0)
	for rows.Next() {
		var item model.SystemEvent
		if err := rows.Scan(&item.ID, &item.EventType, &item.Level, &item.Source, &item.Message, &item.PayloadJSON, &item.CreatedAt); err != nil {
			return nil, false, fmt.Errorf("scan system event: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate system events: %w", err)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return items, hasMore, nil
}

func (r *SystemEventRepository) DeleteBefore(ctx context.Context, cutoff string) (int64, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM system_events WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete system events before %s: %w", cutoff, err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted system events: %w", err)
	}
	return deleted, nil
}

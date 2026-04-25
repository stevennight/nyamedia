package storage

import (
	"context"
	"database/sql"
	"fmt"

	"NyaMedia/internal/model"
)

type TaskLogRepository struct {
	db *sql.DB
}

type TaskLogListOptions struct {
	Limit           int
	BeforeCreatedAt string
	BeforeID        string
	AfterCreatedAt  string
	AfterID         string
}

func NewTaskLogRepository(db *sql.DB) *TaskLogRepository {
	return &TaskLogRepository{db: db}
}

func (r *TaskLogRepository) Create(ctx context.Context, item model.TaskLog) error {
	const query = `
INSERT INTO task_logs (id, task_id, level, message, payload_json)
VALUES (?, ?, ?, ?, NULLIF(?, ''))`

	_, err := r.db.ExecContext(ctx, query, item.ID, item.TaskID, item.Level, item.Message, item.PayloadJSON)
	if err != nil {
		return fmt.Errorf("create task log %s: %w", item.ID, err)
	}
	return nil
}

func (r *TaskLogRepository) ListByTask(ctx context.Context, taskID string, options TaskLogListOptions) ([]model.TaskLog, bool, error) {
	limit := options.Limit
	if limit <= 0 {
		limit = 200
	}
	query := `
SELECT id, task_id, level, message, COALESCE(payload_json, ''), created_at
FROM task_logs
WHERE task_id = ?`
	args := []any{taskID}
	if options.AfterCreatedAt != "" && options.AfterID != "" {
		query += `
  AND (created_at > ? OR (created_at = ? AND id > ?))`
		args = append(args, options.AfterCreatedAt, options.AfterCreatedAt, options.AfterID)
	} else if options.BeforeCreatedAt != "" && options.BeforeID != "" {
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
		return nil, false, fmt.Errorf("list task logs for %s: %w", taskID, err)
	}
	defer rows.Close()

	items := make([]model.TaskLog, 0)
	for rows.Next() {
		var item model.TaskLog
		if err := rows.Scan(&item.ID, &item.TaskID, &item.Level, &item.Message, &item.PayloadJSON, &item.CreatedAt); err != nil {
			return nil, false, fmt.Errorf("scan task log: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate task logs for %s: %w", taskID, err)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return items, hasMore, nil
}

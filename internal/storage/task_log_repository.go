package storage

import (
	"context"
	"database/sql"
	"fmt"

	"emby115/internal/model"
)

type TaskLogRepository struct {
	db *sql.DB
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

func (r *TaskLogRepository) ListByTask(ctx context.Context, taskID string, limit int) ([]model.TaskLog, error) {
	if limit <= 0 {
		limit = 200
	}
	const query = `
SELECT id, task_id, level, message, COALESCE(payload_json, ''), created_at
FROM task_logs
WHERE task_id = ?
ORDER BY created_at ASC, id ASC
LIMIT ?`

	rows, err := r.db.QueryContext(ctx, query, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("list task logs for %s: %w", taskID, err)
	}
	defer rows.Close()

	items := make([]model.TaskLog, 0)
	for rows.Next() {
		var item model.TaskLog
		if err := rows.Scan(&item.ID, &item.TaskID, &item.Level, &item.Message, &item.PayloadJSON, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan task log: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task logs for %s: %w", taskID, err)
	}
	return items, nil
}

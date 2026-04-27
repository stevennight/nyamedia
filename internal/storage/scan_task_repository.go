package storage

import (
	"context"
	"database/sql"
	"fmt"

	"NyaMedia/internal/model"
)

type ScanTaskRepository struct {
	db *sql.DB
}

func NewScanTaskRepository(db *sql.DB) *ScanTaskRepository {
	return &ScanTaskRepository{db: db}
}

func (r *ScanTaskRepository) List(ctx context.Context) ([]model.ScanTask, error) {
	items, _, err := r.ListPage(ctx, 0, 0)
	return items, err
}

func (r *ScanTaskRepository) ListPage(ctx context.Context, limit, offset int) ([]model.ScanTask, int, error) {
	if limit <= 0 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	const countQuery = `SELECT COUNT(1) FROM scan_tasks`
	var total int
	if err := r.db.QueryRowContext(ctx, countQuery).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count scan tasks: %w", err)
	}

	const query = `
SELECT id, task_type, COALESCE(library_id, ''), status, COALESCE(progress_total, 0), COALESCE(progress_done, 0),
       COALESCE(message, ''), COALESCE(error_message, ''), started_at, COALESCE(finished_at, ''), created_at, updated_at
FROM scan_tasks
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?`

	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list scan tasks: %w", err)
	}
	defer rows.Close()

	items := make([]model.ScanTask, 0)
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate scan tasks: %w", err)
	}
	return items, total, nil
}

func (r *ScanTaskRepository) Get(ctx context.Context, id string) (*model.ScanTask, error) {
	const query = `
SELECT id, task_type, COALESCE(library_id, ''), status, COALESCE(progress_total, 0), COALESCE(progress_done, 0),
       COALESCE(message, ''), COALESCE(error_message, ''), started_at, COALESCE(finished_at, ''), created_at, updated_at
FROM scan_tasks
WHERE id = ?`

	item, err := scanTaskRow(r.db.QueryRowContext(ctx, query, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get scan task %s: %w", id, err)
	}
	return item, nil
}

func (r *ScanTaskRepository) Create(ctx context.Context, item model.ScanTask) error {
	const query = `
INSERT INTO scan_tasks (id, task_type, library_id, status, progress_total, progress_done, message, error_message, started_at, finished_at)
VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''))`

	_, err := r.db.ExecContext(ctx, query,
		item.ID,
		item.TaskType,
		item.LibraryID,
		taskStatusOrDefault(item.Status),
		item.ProgressTotal,
		item.ProgressDone,
		item.Message,
		item.ErrorMessage,
		item.StartedAt,
		item.FinishedAt,
	)
	if err != nil {
		return fmt.Errorf("create scan task %s: %w", item.ID, err)
	}
	return nil
}

func (r *ScanTaskRepository) Update(ctx context.Context, item model.ScanTask) error {
	const query = `
UPDATE scan_tasks
SET status = ?,
    progress_total = ?,
    progress_done = ?,
    message = NULLIF(?, ''),
    error_message = NULLIF(?, ''),
    started_at = ?,
    finished_at = NULLIF(?, ''),
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?`

	result, err := r.db.ExecContext(ctx, query,
		taskStatusOrDefault(item.Status),
		item.ProgressTotal,
		item.ProgressDone,
		item.Message,
		item.ErrorMessage,
		item.StartedAt,
		item.FinishedAt,
		item.ID,
	)
	if err != nil {
		return fmt.Errorf("update scan task %s: %w", item.ID, err)
	}
	return ensureRowsAffected(result, "scan task not found")
}

func (r *ScanTaskRepository) FindActive(ctx context.Context, taskType, libraryID string) (*model.ScanTask, error) {
	query := `
SELECT id, task_type, COALESCE(library_id, ''), status, COALESCE(progress_total, 0), COALESCE(progress_done, 0),
       COALESCE(message, ''), COALESCE(error_message, ''), started_at, COALESCE(finished_at, ''), created_at, updated_at
FROM scan_tasks
WHERE task_type = ?
  AND status IN ('pending', 'running')`
	args := []any{taskType}
	if libraryID == "" {
		query += " AND (library_id IS NULL OR library_id = '')"
	} else {
		query += " AND library_id = ?"
		args = append(args, libraryID)
	}
	query += " ORDER BY created_at DESC LIMIT 1"

	item, err := scanTaskRow(r.db.QueryRowContext(ctx, query, args...))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find active scan task: %w", err)
	}
	return item, nil
}

func (r *ScanTaskRepository) FindActiveByType(ctx context.Context, taskType string) (*model.ScanTask, error) {
	const query = `
SELECT id, task_type, COALESCE(library_id, ''), status, COALESCE(progress_total, 0), COALESCE(progress_done, 0),
       COALESCE(message, ''), COALESCE(error_message, ''), started_at, COALESCE(finished_at, ''), created_at, updated_at
FROM scan_tasks
WHERE task_type = ?
  AND status IN ('pending', 'running')
ORDER BY created_at DESC LIMIT 1`

	item, err := scanTaskRow(r.db.QueryRowContext(ctx, query, taskType))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find active scan task by type: %w", err)
	}
	return item, nil
}

func (r *ScanTaskRepository) CountActiveByType(ctx context.Context, taskType string) (int, error) {
	const query = `
SELECT COUNT(1)
FROM scan_tasks
WHERE task_type = ?
  AND status IN ('pending', 'running')`

	var count int
	if err := r.db.QueryRowContext(ctx, query, taskType).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active scan tasks by type: %w", err)
	}
	return count, nil
}

func (r *ScanTaskRepository) DeleteFinishedBefore(ctx context.Context, cutoff string) (int64, error) {
	const query = `
DELETE FROM scan_tasks
WHERE status NOT IN ('pending', 'running')
  AND COALESCE(NULLIF(finished_at, ''), updated_at, created_at) < ?`

	result, err := r.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete finished scan tasks before %s: %w", cutoff, err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted scan tasks: %w", err)
	}
	return deleted, nil
}

func scanTask(scanner interface{ Scan(dest ...any) error }) (model.ScanTask, error) {
	itemPtr, err := scanTaskRow(scanner)
	if err != nil {
		return model.ScanTask{}, err
	}
	return *itemPtr, nil
}

func scanTaskRow(scanner interface{ Scan(dest ...any) error }) (*model.ScanTask, error) {
	var item model.ScanTask
	err := scanner.Scan(
		&item.ID,
		&item.TaskType,
		&item.LibraryID,
		&item.Status,
		&item.ProgressTotal,
		&item.ProgressDone,
		&item.Message,
		&item.ErrorMessage,
		&item.StartedAt,
		&item.FinishedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func taskStatusOrDefault(status model.TaskStatus) model.TaskStatus {
	if status == "" {
		return model.TaskStatusPending
	}
	return status
}

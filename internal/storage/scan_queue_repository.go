package storage

import (
	"context"
	"database/sql"
	"fmt"

	"NyaMedia/internal/model"
)

type ScanQueueRepository struct {
	db *sql.DB
}

func NewScanQueueRepository(db *sql.DB) *ScanQueueRepository {
	return &ScanQueueRepository{db: db}
}

func (r *ScanQueueRepository) List(ctx context.Context) ([]model.ScanQueueItem, error) {
	const query = `
SELECT id, library_id, COALESCE(mount_id, ''), provider_id, source_path, mode, source, run_after, status,
       event_count, last_event_at, COALESCE(options_json, ''), COALESCE(reason_json, ''), created_at, updated_at
FROM scan_queue
ORDER BY run_after ASC, created_at ASC, id ASC`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list scan queue: %w", err)
	}
	defer rows.Close()

	items := make([]model.ScanQueueItem, 0)
	for rows.Next() {
		item, err := scanQueueItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scan queue: %w", err)
	}
	return items, nil
}

func (r *ScanQueueRepository) Upsert(ctx context.Context, item model.ScanQueueItem) (*model.ScanQueueItem, error) {
	const query = `
INSERT INTO scan_queue (id, library_id, mount_id, provider_id, source_path, mode, source, run_after, status, event_count, last_event_at, options_json, reason_json)
VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, 'pending', 1, ?, NULLIF(?, ''), NULLIF(?, ''))
ON CONFLICT (library_id, provider_id, source_path, mode) DO UPDATE SET
    mount_id = COALESCE(NULLIF(EXCLUDED.mount_id, ''), scan_queue.mount_id),
    source = EXCLUDED.source,
    run_after = GREATEST(scan_queue.run_after, EXCLUDED.run_after),
    status = 'pending',
    event_count = scan_queue.event_count + 1,
    last_event_at = EXCLUDED.last_event_at,
    options_json = COALESCE(EXCLUDED.options_json, scan_queue.options_json),
    reason_json = COALESCE(EXCLUDED.reason_json, scan_queue.reason_json),
    updated_at = CURRENT_TIMESTAMP
RETURNING id, library_id, COALESCE(mount_id, ''), provider_id, source_path, mode, source, run_after, status,
          event_count, last_event_at, COALESCE(options_json, ''), COALESCE(reason_json, ''), created_at, updated_at`
	created, err := scanQueueItemRow(r.db.QueryRowContext(ctx, query,
		item.ID,
		item.LibraryID,
		item.MountID,
		item.ProviderID,
		item.SourcePath,
		item.Mode,
		item.Source,
		item.RunAfter,
		item.LastEventAt,
		item.OptionsJSON,
		item.ReasonJSON,
	))
	if err != nil {
		return nil, fmt.Errorf("upsert scan queue item: %w", err)
	}
	return created, nil
}

func (r *ScanQueueRepository) FindCoveringRecursive(ctx context.Context, libraryID, providerID, sourcePath string) (*model.ScanQueueItem, error) {
	const query = `
SELECT id, library_id, COALESCE(mount_id, ''), provider_id, source_path, mode, source, run_after, status,
       event_count, last_event_at, COALESCE(options_json, ''), COALESCE(reason_json, ''), created_at, updated_at
FROM scan_queue
WHERE library_id = ?
  AND provider_id = ?
  AND mode = 'recursive'
  AND status = 'pending'
  AND source_path <> ?
  AND (source_path = '/' OR ? = source_path OR ? LIKE source_path || '/%')
ORDER BY length(source_path) DESC
LIMIT 1`
	item, err := scanQueueItemRow(r.db.QueryRowContext(ctx, query, libraryID, providerID, sourcePath, sourcePath, sourcePath))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find covering recursive scan queue item: %w", err)
	}
	return item, nil
}

func (r *ScanQueueRepository) Touch(ctx context.Context, id, source, lastEventAt, reasonJSON string) (*model.ScanQueueItem, error) {
	const query = `
UPDATE scan_queue
SET source = ?,
    event_count = event_count + 1,
    last_event_at = ?,
    reason_json = COALESCE(NULLIF(?, ''), reason_json),
    updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND status = 'pending'
RETURNING id, library_id, COALESCE(mount_id, ''), provider_id, source_path, mode, source, run_after, status,
          event_count, last_event_at, COALESCE(options_json, ''), COALESCE(reason_json, ''), created_at, updated_at`
	item, err := scanQueueItemRow(r.db.QueryRowContext(ctx, query, source, lastEventAt, reasonJSON, id))
	if err != nil {
		return nil, fmt.Errorf("touch scan queue item %s: %w", id, err)
	}
	return item, nil
}

func (r *ScanQueueRepository) DeleteCovered(ctx context.Context, libraryID, providerID, sourcePath string) error {
	const query = `
DELETE FROM scan_queue
WHERE library_id = ?
  AND provider_id = ?
  AND source_path <> ?
  AND (source_path = ? OR source_path LIKE ?)
  AND status = 'pending'`
	likePrefix := sourcePath
	if likePrefix != "/" {
		likePrefix += "/%"
	} else {
		likePrefix = "/%"
	}
	if _, err := r.db.ExecContext(ctx, query, libraryID, providerID, sourcePath, sourcePath, likePrefix); err != nil {
		return fmt.Errorf("delete covered scan queue items: %w", err)
	}
	return nil
}

func (r *ScanQueueRepository) FirstDue(ctx context.Context, now string) (*model.ScanQueueItem, error) {
	const query = `
SELECT id, library_id, COALESCE(mount_id, ''), provider_id, source_path, mode, source, run_after, status,
       event_count, last_event_at, COALESCE(options_json, ''), COALESCE(reason_json, ''), created_at, updated_at
FROM scan_queue
WHERE status = 'pending' AND run_after <= ?
ORDER BY run_after ASC, created_at ASC, id ASC
LIMIT 1`
	item, err := scanQueueItemRow(r.db.QueryRowContext(ctx, query, now))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get due scan queue item: %w", err)
	}
	return item, nil
}

func (r *ScanQueueRepository) Delay(ctx context.Context, id, runAfter string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE scan_queue SET run_after = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND status = 'pending'`, runAfter, id)
	if err != nil {
		return fmt.Errorf("delay scan queue item %s: %w", id, err)
	}
	return ensureRowsAffected(result, "scan queue item not found")
}

func (r *ScanQueueRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM scan_queue WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete scan queue item %s: %w", id, err)
	}
	return ensureRowsAffected(result, "scan queue item not found")
}

func (r *ScanQueueRepository) DeleteAndCreateTask(ctx context.Context, queueID string, task model.ScanTask) (*model.ScanTask, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin dequeue scan queue item %s: %w", queueID, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	result, err := tx.ExecContext(ctx, `DELETE FROM scan_queue WHERE id = ? AND status = 'pending'`, queueID)
	if err != nil {
		return nil, fmt.Errorf("delete scan queue item %s: %w", queueID, err)
	}
	if err = ensureRowsAffected(result, "scan queue item not found"); err != nil {
		return nil, err
	}

	const insertTask = `
INSERT INTO scan_tasks (id, task_type, library_id, status, progress_total, progress_done, message, error_message, started_at, finished_at)
VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''))`
	if _, err = tx.ExecContext(ctx, insertTask,
		task.ID,
		task.TaskType,
		task.LibraryID,
		taskStatusOrDefault(task.Status),
		task.ProgressTotal,
		task.ProgressDone,
		task.Message,
		task.ErrorMessage,
		task.StartedAt,
		task.FinishedAt,
	); err != nil {
		return nil, fmt.Errorf("create scan task %s: %w", task.ID, err)
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit dequeue scan queue item %s: %w", queueID, err)
	}
	return r.getTask(ctx, task.ID)
}

func (r *ScanQueueRepository) getTask(ctx context.Context, id string) (*model.ScanTask, error) {
	const query = `
SELECT id, task_type, COALESCE(library_id, ''), status, COALESCE(progress_total, 0), COALESCE(progress_done, 0),
       COALESCE(message, ''), COALESCE(error_message, ''), started_at, COALESCE(finished_at, ''), created_at, updated_at
FROM scan_tasks
WHERE id = ?`
	item, err := scanTaskRow(r.db.QueryRowContext(ctx, query, id))
	if err != nil {
		return nil, fmt.Errorf("get scan task %s: %w", id, err)
	}
	return item, nil
}

func scanQueueItem(scanner interface{ Scan(dest ...any) error }) (model.ScanQueueItem, error) {
	itemPtr, err := scanQueueItemRow(scanner)
	if err != nil {
		return model.ScanQueueItem{}, err
	}
	return *itemPtr, nil
}

func scanQueueItemRow(scanner interface{ Scan(dest ...any) error }) (*model.ScanQueueItem, error) {
	var item model.ScanQueueItem
	if err := scanner.Scan(&item.ID, &item.LibraryID, &item.MountID, &item.ProviderID, &item.SourcePath, &item.Mode, &item.Source, &item.RunAfter, &item.Status, &item.EventCount, &item.LastEventAt, &item.OptionsJSON, &item.ReasonJSON, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return nil, err
	}
	return &item, nil
}

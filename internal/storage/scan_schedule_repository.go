package storage

import (
	"context"
	"database/sql"
	"fmt"

	"NyaMedia/internal/model"
)

type ScanScheduleRepository struct {
	db *sql.DB
}

func NewScanScheduleRepository(db *sql.DB) *ScanScheduleRepository {
	return &ScanScheduleRepository{db: db}
}

func (r *ScanScheduleRepository) List(ctx context.Context) ([]model.ScanSchedule, error) {
	const query = `
SELECT id, name, library_id, COALESCE(mount_id, ''), COALESCE(source_path, ''), cron, enabled,
       COALESCE(last_run_at, ''), created_at, updated_at
FROM scan_schedules
ORDER BY name, id`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list scan schedules: %w", err)
	}
	defer rows.Close()

	items := make([]model.ScanSchedule, 0)
	for rows.Next() {
		item, err := scanScanSchedule(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scan schedules: %w", err)
	}
	return items, nil
}

func (r *ScanScheduleRepository) ListEnabled(ctx context.Context) ([]model.ScanSchedule, error) {
	const query = `
SELECT id, name, library_id, COALESCE(mount_id, ''), COALESCE(source_path, ''), cron, enabled,
       COALESCE(last_run_at, ''), created_at, updated_at
FROM scan_schedules
WHERE enabled = 1
ORDER BY name, id`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list enabled scan schedules: %w", err)
	}
	defer rows.Close()

	items := make([]model.ScanSchedule, 0)
	for rows.Next() {
		item, err := scanScanSchedule(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enabled scan schedules: %w", err)
	}
	return items, nil
}

func (r *ScanScheduleRepository) Get(ctx context.Context, id string) (*model.ScanSchedule, error) {
	const query = `
SELECT id, name, library_id, COALESCE(mount_id, ''), COALESCE(source_path, ''), cron, enabled,
       COALESCE(last_run_at, ''), created_at, updated_at
FROM scan_schedules
WHERE id = ?`

	item, err := scanScanScheduleRow(r.db.QueryRowContext(ctx, query, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get scan schedule %s: %w", id, err)
	}
	return item, nil
}

func (r *ScanScheduleRepository) Create(ctx context.Context, item model.ScanSchedule) error {
	const query = `
INSERT INTO scan_schedules (id, name, library_id, mount_id, source_path, cron, enabled, last_run_at)
VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, NULLIF(?, ''))`

	_, err := r.db.ExecContext(ctx, query,
		item.ID,
		item.Name,
		item.LibraryID,
		item.MountID,
		item.SourcePath,
		item.Cron,
		boolToInt(item.Enabled),
		item.LastRunAt,
	)
	if err != nil {
		return fmt.Errorf("create scan schedule %s: %w", item.ID, err)
	}
	return nil
}

func (r *ScanScheduleRepository) Update(ctx context.Context, item model.ScanSchedule) error {
	const query = `
UPDATE scan_schedules
SET name = ?,
    library_id = ?,
    mount_id = NULLIF(?, ''),
    source_path = NULLIF(?, ''),
    cron = ?,
    enabled = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?`

	result, err := r.db.ExecContext(ctx, query,
		item.Name,
		item.LibraryID,
		item.MountID,
		item.SourcePath,
		item.Cron,
		boolToInt(item.Enabled),
		item.ID,
	)
	if err != nil {
		return fmt.Errorf("update scan schedule %s: %w", item.ID, err)
	}
	return ensureRowsAffected(result, "scan schedule not found")
}

func (r *ScanScheduleRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM scan_schedules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete scan schedule %s: %w", id, err)
	}
	return ensureRowsAffected(result, "scan schedule not found")
}

func (r *ScanScheduleRepository) MarkRun(ctx context.Context, id, minuteKey string) (bool, error) {
	const query = `
UPDATE scan_schedules
SET last_run_at = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND enabled = 1 AND COALESCE(last_run_at, '') <> ?`
	result, err := r.db.ExecContext(ctx, query, minuteKey, id, minuteKey)
	if err != nil {
		return false, fmt.Errorf("mark scan schedule %s run: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read scan schedule %s mark result: %w", id, err)
	}
	return rows > 0, nil
}

func scanScanSchedule(scanner interface{ Scan(dest ...any) error }) (model.ScanSchedule, error) {
	itemPtr, err := scanScanScheduleRow(scanner)
	if err != nil {
		return model.ScanSchedule{}, err
	}
	return *itemPtr, nil
}

func scanScanScheduleRow(scanner interface{ Scan(dest ...any) error }) (*model.ScanSchedule, error) {
	var item model.ScanSchedule
	var enabled int
	if err := scanner.Scan(
		&item.ID,
		&item.Name,
		&item.LibraryID,
		&item.MountID,
		&item.SourcePath,
		&item.Cron,
		&enabled,
		&item.LastRunAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return nil, err
	}
	item.Enabled = enabled == 1
	return &item, nil
}

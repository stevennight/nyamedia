package storage

import (
	"context"
	"database/sql"
	"fmt"

	"NyaMedia/internal/model"
)

type LibraryRepository struct {
	db *sql.DB
}

func NewLibraryRepository(db *sql.DB) *LibraryRepository {
	return &LibraryRepository{db: db}
}

func (r *LibraryRepository) List(ctx context.Context) ([]model.Library, error) {
	const query = `
SELECT id, name, COALESCE(description, ''), enabled, COALESCE(last_scan_at, ''), COALESCE(scan_cron, ''), created_at, updated_at
FROM libraries
ORDER BY id`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
	}
	defer rows.Close()

	items := make([]model.Library, 0)
	for rows.Next() {
		item, err := scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate libraries: %w", err)
	}

	return items, nil
}

func (r *LibraryRepository) Count(ctx context.Context) (int, error) {
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM libraries`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count libraries: %w", err)
	}
	return count, nil
}

func (r *LibraryRepository) ListEnabled(ctx context.Context) ([]model.Library, error) {
	const query = `
SELECT id, name, COALESCE(description, ''), enabled, COALESCE(last_scan_at, ''), COALESCE(scan_cron, ''), created_at, updated_at
FROM libraries
WHERE enabled = 1
ORDER BY id`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list enabled libraries: %w", err)
	}
	defer rows.Close()

	items := make([]model.Library, 0)
	for rows.Next() {
		item, err := scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enabled libraries: %w", err)
	}
	return items, nil
}

func (r *LibraryRepository) Get(ctx context.Context, id string) (*model.Library, error) {
	const query = `
SELECT id, name, COALESCE(description, ''), enabled, COALESCE(last_scan_at, ''), COALESCE(scan_cron, ''), created_at, updated_at
FROM libraries
WHERE id = ?`

	item, err := scanLibraryRow(r.db.QueryRowContext(ctx, query, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get library %s: %w", id, err)
	}
	return item, nil
}

func (r *LibraryRepository) Create(ctx context.Context, item model.Library) error {
	const query = `
INSERT INTO libraries (id, name, description, enabled, last_scan_at, scan_cron)
VALUES (?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''))`

	_, err := r.db.ExecContext(ctx, query, item.ID, item.Name, item.Description, boolToInt(item.Enabled), item.LastScanAt, item.ScanCron)
	if err != nil {
		return fmt.Errorf("create library %s: %w", item.ID, err)
	}
	return nil
}

func (r *LibraryRepository) Update(ctx context.Context, item model.Library) error {
	const query = `
UPDATE libraries
SET name = ?,
    description = NULLIF(?, ''),
    enabled = ?,
    last_scan_at = NULLIF(?, ''),
    scan_cron = NULLIF(?, ''),
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?`

	result, err := r.db.ExecContext(ctx, query, item.Name, item.Description, boolToInt(item.Enabled), item.LastScanAt, item.ScanCron, item.ID)
	if err != nil {
		return fmt.Errorf("update library %s: %w", item.ID, err)
	}
	return ensureRowsAffected(result, "library not found")
}

func (r *LibraryRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM libraries WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete library %s: %w", id, err)
	}
	return ensureRowsAffected(result, "library not found")
}

func (r *LibraryRepository) MarkScanned(ctx context.Context, id, scannedAt string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE libraries SET last_scan_at = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, scannedAt, id)
	if err != nil {
		return fmt.Errorf("mark library scanned %s: %w", id, err)
	}
	return ensureRowsAffected(result, "library not found")
}

func (r *LibraryRepository) ListMounts(ctx context.Context, libraryID string) ([]model.LibraryMount, error) {
	const query = `
SELECT id, library_id, provider_id, source_path, target_path, COALESCE(media_type, ''), priority, enabled, created_at, updated_at
FROM library_mounts
WHERE library_id = ?
ORDER BY priority, id`

	rows, err := r.db.QueryContext(ctx, query, libraryID)
	if err != nil {
		return nil, fmt.Errorf("list mounts for library %s: %w", libraryID, err)
	}
	defer rows.Close()

	items := make([]model.LibraryMount, 0)
	for rows.Next() {
		item, err := scanLibraryMount(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mounts for library %s: %w", libraryID, err)
	}
	return items, nil
}

func (r *LibraryRepository) ListEnabledMounts(ctx context.Context, libraryID string) ([]model.LibraryMount, error) {
	const query = `
SELECT id, library_id, provider_id, source_path, target_path, COALESCE(media_type, ''), priority, enabled, created_at, updated_at
FROM library_mounts
WHERE library_id = ? AND enabled = 1
ORDER BY priority, id`

	rows, err := r.db.QueryContext(ctx, query, libraryID)
	if err != nil {
		return nil, fmt.Errorf("list enabled mounts for library %s: %w", libraryID, err)
	}
	defer rows.Close()

	items := make([]model.LibraryMount, 0)
	for rows.Next() {
		item, err := scanLibraryMount(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enabled mounts for library %s: %w", libraryID, err)
	}
	return items, nil
}

func (r *LibraryRepository) CreateMount(ctx context.Context, mount model.LibraryMount) error {
	const query = `
INSERT INTO library_mounts (id, library_id, provider_id, source_path, target_path, media_type, priority, enabled)
VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`

	_, err := r.db.ExecContext(ctx, query,
		mount.ID,
		mount.LibraryID,
		mount.ProviderID,
		mount.SourcePath,
		mount.TargetPath,
		mount.MediaType,
		mount.Priority,
		boolToInt(mount.Enabled),
	)
	if err != nil {
		return fmt.Errorf("create mount %s: %w", mount.ID, err)
	}
	return nil
}

func (r *LibraryRepository) UpdateMount(ctx context.Context, mount model.LibraryMount) error {
	const query = `
UPDATE library_mounts
SET provider_id = ?,
    source_path = ?,
    target_path = ?,
    media_type = NULLIF(?, ''),
    priority = ?,
    enabled = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE library_id = ? AND id = ?`

	result, err := r.db.ExecContext(ctx, query,
		mount.ProviderID,
		mount.SourcePath,
		mount.TargetPath,
		mount.MediaType,
		mount.Priority,
		boolToInt(mount.Enabled),
		mount.LibraryID,
		mount.ID,
	)
	if err != nil {
		return fmt.Errorf("update mount %s: %w", mount.ID, err)
	}
	return ensureRowsAffected(result, "mount not found")
}

func (r *LibraryRepository) DeleteMount(ctx context.Context, libraryID, mountID string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM library_mounts WHERE library_id = ? AND id = ?`, libraryID, mountID)
	if err != nil {
		return fmt.Errorf("delete mount %s: %w", mountID, err)
	}
	return ensureRowsAffected(result, "mount not found")
}

func (r *LibraryRepository) CountMountsByProvider(ctx context.Context, providerID string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM library_mounts WHERE provider_id = ?`, providerID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count mounts by provider %s: %w", providerID, err)
	}
	return count, nil
}

func scanLibrary(scanner interface{ Scan(dest ...any) error }) (model.Library, error) {
	itemPtr, err := scanLibraryRow(scanner)
	if err != nil {
		return model.Library{}, err
	}
	return *itemPtr, nil
}

func scanLibraryRow(scanner interface{ Scan(dest ...any) error }) (*model.Library, error) {
	var item model.Library
	var enabled int
	err := scanner.Scan(&item.ID, &item.Name, &item.Description, &enabled, &item.LastScanAt, &item.ScanCron, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	item.Enabled = enabled == 1
	return &item, nil
}

func scanLibraryMount(scanner interface{ Scan(dest ...any) error }) (model.LibraryMount, error) {
	var item model.LibraryMount
	var enabled int
	err := scanner.Scan(
		&item.ID,
		&item.LibraryID,
		&item.ProviderID,
		&item.SourcePath,
		&item.TargetPath,
		&item.MediaType,
		&item.Priority,
		&enabled,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return model.LibraryMount{}, err
	}
	item.Enabled = enabled == 1
	return item, nil
}

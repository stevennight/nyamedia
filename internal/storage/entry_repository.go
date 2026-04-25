package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"emby115/internal/model"
)

type EntryRepository struct {
	db *sql.DB
}

func NewEntryRepository(db *sql.DB) *EntryRepository {
	return &EntryRepository{db: db}
}

func (r *EntryRepository) Upsert(ctx context.Context, item model.Entry) error {
	const query = `
INSERT INTO entries (id, provider_id, entry_type, path, parent_path, name, size, mtime, mime_type, content_hash, provider_entry_id, metadata_json, last_seen_at)
VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?)
ON CONFLICT(provider_id, path) DO UPDATE SET
    id = excluded.id,
    entry_type = excluded.entry_type,
    parent_path = excluded.parent_path,
    name = excluded.name,
    size = excluded.size,
    mtime = excluded.mtime,
    mime_type = excluded.mime_type,
    content_hash = excluded.content_hash,
	provider_entry_id = excluded.provider_entry_id,
	metadata_json = excluded.metadata_json,
    last_seen_at = excluded.last_seen_at,
    updated_at = CURRENT_TIMESTAMP`

	_, err := r.db.ExecContext(ctx, query,
		item.ID,
		item.ProviderID,
		item.EntryType,
		item.Path,
		item.ParentPath,
		item.Name,
		item.Size,
		item.MTime,
		item.MimeType,
		item.ContentHash,
		item.ProviderEntryID,
		item.MetadataJSON,
		item.LastSeenAt,
	)
	if err != nil {
		return fmt.Errorf("upsert entry %s/%s: %w", item.ProviderID, item.Path, err)
	}
	return nil
}

func (r *EntryRepository) Get(ctx context.Context, providerID, entryPath string) (*model.Entry, error) {
	const query = `
SELECT id, provider_id, entry_type, path, COALESCE(parent_path, ''), name, COALESCE(size, 0),
       COALESCE(mtime, ''), COALESCE(mime_type, ''), COALESCE(content_hash, ''),
       COALESCE(provider_entry_id, ''), COALESCE(metadata_json, ''),
       last_seen_at, created_at, updated_at
FROM entries
WHERE provider_id = ? AND path = ?`

	var item model.Entry
	err := r.db.QueryRowContext(ctx, query, providerID, entryPath).Scan(
		&item.ID,
		&item.ProviderID,
		&item.EntryType,
		&item.Path,
		&item.ParentPath,
		&item.Name,
		&item.Size,
		&item.MTime,
		&item.MimeType,
		&item.ContentHash,
		&item.ProviderEntryID,
		&item.MetadataJSON,
		&item.LastSeenAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get entry %s/%s: %w", providerID, entryPath, err)
	}
	return &item, nil
}

func (r *EntryRepository) DeleteStaleUnderPrefix(ctx context.Context, providerID, prefix, lastSeenAt string) error {
	query := `
DELETE FROM entries
WHERE provider_id = ?
  AND last_seen_at <> ?`
	args := []any{providerID, lastSeenAt}
	if prefix == "/" {
		query += `
  AND path LIKE ?`
		args = append(args, "/%")
	} else {
		query += `
  AND (path = ? OR path LIKE ?)`
		args = append(args, prefix, prefix+"/%")
	}

	if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("delete stale entries for %s under %s: %w", providerID, prefix, err)
	}
	return nil
}

func (r *EntryRepository) List(ctx context.Context, providerID, prefix string, limit int) ([]model.Entry, error) {
	items, _, err := r.ListPage(ctx, providerID, prefix, limit, 0)
	return items, err
}

func (r *EntryRepository) ListPage(ctx context.Context, providerID, prefix string, limit, offset int) ([]model.Entry, int, error) {
	if limit <= 0 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	baseQuery := `
SELECT id, provider_id, entry_type, path, COALESCE(parent_path, ''), name, COALESCE(size, 0),
       COALESCE(mtime, ''), COALESCE(mime_type, ''), COALESCE(content_hash, ''),
       COALESCE(provider_entry_id, ''), COALESCE(metadata_json, ''),
       last_seen_at, created_at, updated_at
FROM entries`
	countQuery := `SELECT COUNT(*) FROM entries`

	conditions := make([]string, 0, 2)
	args := make([]any, 0, 4)
	if providerID != "" {
		conditions = append(conditions, "provider_id = ?")
		args = append(args, providerID)
	}
	if prefix != "" {
		conditions = append(conditions, "path LIKE ?")
		args = append(args, prefix+"%")
	}
	query := baseQuery
	if len(conditions) > 0 {
		clause := "\nWHERE " + strings.Join(conditions, " AND ")
		query += clause
		countQuery += clause
	}

	var total int
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count entries: %w", err)
	}

	query += "\nORDER BY updated_at DESC, provider_id, path LIMIT ? OFFSET ?"
	args = append(args, limit)
	args = append(args, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()

	items := make([]model.Entry, 0)
	for rows.Next() {
		var item model.Entry
		if err := rows.Scan(
			&item.ID,
			&item.ProviderID,
			&item.EntryType,
			&item.Path,
			&item.ParentPath,
			&item.Name,
			&item.Size,
			&item.MTime,
			&item.MimeType,
			&item.ContentHash,
			&item.ProviderEntryID,
			&item.MetadataJSON,
			&item.LastSeenAt,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan entry: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate entries: %w", err)
	}
	return items, total, nil
}

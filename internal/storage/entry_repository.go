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
INSERT INTO entries (id, provider_id, entry_type, path, parent_path, name, size, mtime, mime_type, content_hash, last_seen_at)
VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?)
ON CONFLICT(provider_id, path) DO UPDATE SET
    id = excluded.id,
    entry_type = excluded.entry_type,
    parent_path = excluded.parent_path,
    name = excluded.name,
    size = excluded.size,
    mtime = excluded.mtime,
    mime_type = excluded.mime_type,
    content_hash = excluded.content_hash,
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
		item.LastSeenAt,
	)
	if err != nil {
		return fmt.Errorf("upsert entry %s/%s: %w", item.ProviderID, item.Path, err)
	}
	return nil
}

func (r *EntryRepository) DeleteStaleUnderPrefix(ctx context.Context, providerID, prefix, lastSeenAt string) error {
	const query = `
DELETE FROM entries
WHERE provider_id = ?
  AND path LIKE ?
  AND last_seen_at <> ?`

	likePrefix := prefix
	if likePrefix == "/" {
		likePrefix = "/%"
	} else {
		likePrefix = prefix + "%"
	}

	if _, err := r.db.ExecContext(ctx, query, providerID, likePrefix, lastSeenAt); err != nil {
		return fmt.Errorf("delete stale entries for %s under %s: %w", providerID, prefix, err)
	}
	return nil
}

func (r *EntryRepository) List(ctx context.Context, providerID, prefix string, limit int) ([]model.Entry, error) {
	if limit <= 0 {
		limit = 200
	}

	query := `
SELECT id, provider_id, entry_type, path, COALESCE(parent_path, ''), name, COALESCE(size, 0),
       COALESCE(mtime, ''), COALESCE(mime_type, ''), COALESCE(content_hash, ''),
       last_seen_at, created_at, updated_at
FROM entries`

	conditions := make([]string, 0, 2)
	args := make([]any, 0, 3)
	if providerID != "" {
		conditions = append(conditions, "provider_id = ?")
		args = append(args, providerID)
	}
	if prefix != "" {
		conditions = append(conditions, "path LIKE ?")
		args = append(args, prefix+"%")
	}
	if len(conditions) > 0 {
		query += "\nWHERE " + strings.Join(conditions, " AND ")
	}
	query += "\nORDER BY updated_at DESC, provider_id, path LIMIT ?"
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
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
			&item.LastSeenAt,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan entry: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entries: %w", err)
	}
	return items, nil
}

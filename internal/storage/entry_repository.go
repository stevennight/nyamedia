package storage

import (
	"context"
	"database/sql"
	"fmt"

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

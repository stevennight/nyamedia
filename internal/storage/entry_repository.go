package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"NyaMedia/internal/model"
)

type EntryRepository struct {
	db *sql.DB
}

type EntryListOptions struct {
	ProviderID       string
	Prefix           string
	Limit            int
	CursorUpdatedAt  string
	CursorProviderID string
	CursorPath       string
}

var likePatternEscaper = strings.NewReplacer(
	`\`, `\\`,
	`%`, `\%`,
	`_`, `\_`,
)

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
  AND path LIKE ? ESCAPE '\'`
		args = append(args, "/%")
	} else {
		query += `
  AND (path = ? OR path LIKE ? ESCAPE '\')`
		args = append(args, prefix, escapeLikePattern(prefix)+"/%")
	}

	if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("delete stale entries for %s under %s: %w", providerID, prefix, err)
	}
	return nil
}

func (r *EntryRepository) DeleteUnderPrefix(ctx context.Context, providerID, prefix string) error {
	query := `
DELETE FROM entries
WHERE provider_id = ?`
	args := []any{providerID}
	if prefix == "/" {
		query += `
  AND path LIKE ? ESCAPE '\'`
		args = append(args, "/%")
	} else {
		query += `
  AND (path = ? OR path LIKE ? ESCAPE '\')`
		args = append(args, prefix, escapeLikePattern(prefix)+"/%")
	}

	if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("delete entries for %s under %s: %w", providerID, prefix, err)
	}
	return nil
}

func (r *EntryRepository) DeletePath(ctx context.Context, providerID, entryPath string) error {
	const query = `
DELETE FROM entries
WHERE provider_id = ? AND path = ?`
	if _, err := r.db.ExecContext(ctx, query, providerID, entryPath); err != nil {
		return fmt.Errorf("delete entry %s/%s: %w", providerID, entryPath, err)
	}
	return nil
}

func (r *EntryRepository) List(ctx context.Context, providerID, prefix string, limit int) ([]model.Entry, error) {
	items, _, err := r.ListCursor(ctx, EntryListOptions{
		ProviderID: providerID,
		Prefix:     prefix,
		Limit:      limit,
	})
	return items, err
}

func (r *EntryRepository) ListUnderPrefix(ctx context.Context, providerID, prefix string) ([]model.Entry, error) {
	query := `
SELECT id, provider_id, entry_type, path, COALESCE(parent_path, ''), name, COALESCE(size, 0),
       COALESCE(mtime, ''), COALESCE(mime_type, ''), COALESCE(content_hash, ''),
       COALESCE(provider_entry_id, ''), COALESCE(metadata_json, ''),
       last_seen_at, created_at, updated_at
FROM entries
WHERE provider_id = ?`
	args := []any{providerID}
	if prefix == "/" {
		query += `
  AND path LIKE ? ESCAPE '\'`
		args = append(args, "/%")
	} else {
		query += `
  AND (path = ? OR path LIKE ? ESCAPE '\')`
		args = append(args, prefix, escapeLikePattern(prefix)+"/%")
	}
	query += `
ORDER BY path`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list entries under prefix: %w", err)
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
			return nil, fmt.Errorf("scan entry: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entries: %w", err)
	}
	return items, nil
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
		conditions = append(conditions, `path LIKE ? ESCAPE '\'`)
		args = append(args, escapeLikePattern(prefix)+"%")
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

func (r *EntryRepository) ListCursor(ctx context.Context, options EntryListOptions) ([]model.Entry, bool, error) {
	limit := options.Limit
	if limit <= 0 {
		limit = 200
	}
	cursorParts := 0
	for _, part := range []string{options.CursorUpdatedAt, options.CursorProviderID, options.CursorPath} {
		if part != "" {
			cursorParts++
		}
	}
	if cursorParts != 0 && cursorParts != 3 {
		return nil, false, fmt.Errorf("entry cursor requires updated_at, provider_id, and path")
	}

	query := `
SELECT id, provider_id, entry_type, path, COALESCE(parent_path, ''), name, COALESCE(size, 0),
       COALESCE(mtime, ''), COALESCE(mime_type, ''), COALESCE(content_hash, ''),
       COALESCE(provider_entry_id, ''), COALESCE(metadata_json, ''),
       last_seen_at, created_at, updated_at
FROM entries`
	conditions := make([]string, 0, 3)
	args := make([]any, 0, 9)
	if options.ProviderID != "" {
		conditions = append(conditions, "provider_id = ?")
		args = append(args, options.ProviderID)
	}
	if options.Prefix != "" {
		conditions = append(conditions, `path LIKE ? ESCAPE '\'`)
		args = append(args, escapeLikePattern(options.Prefix)+"%")
	}
	if cursorParts == 3 {
		conditions = append(conditions, `(updated_at < ? OR (updated_at = ? AND (provider_id > ? OR (provider_id = ? AND path > ?))))`)
		args = append(args,
			options.CursorUpdatedAt,
			options.CursorUpdatedAt,
			options.CursorProviderID,
			options.CursorProviderID,
			options.CursorPath,
		)
	}
	if len(conditions) > 0 {
		query += "\nWHERE " + strings.Join(conditions, " AND ")
	}
	query += "\nORDER BY updated_at DESC, provider_id, path LIMIT ?"
	args = append(args, limit+1)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("list entries by cursor: %w", err)
	}
	defer rows.Close()

	items := make([]model.Entry, 0, limit+1)
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
			return nil, false, fmt.Errorf("scan entry: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate entries: %w", err)
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return items, hasMore, nil
}

func escapeLikePattern(value string) string {
	return likePatternEscaper.Replace(value)
}

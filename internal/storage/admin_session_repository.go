package storage

import (
	"context"
	"database/sql"
	"fmt"

	"emby115/internal/model"
)

type AdminSessionRepository struct {
	db *sql.DB
}

func NewAdminSessionRepository(db *sql.DB) *AdminSessionRepository {
	return &AdminSessionRepository{db: db}
}

func (r *AdminSessionRepository) Create(ctx context.Context, token, userID, expiresAt string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO admin_sessions (token, user_id, expires_at, last_seen_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, token, userID, expiresAt)
	if err != nil {
		return fmt.Errorf("create admin session: %w", err)
	}
	return nil
}

func (r *AdminSessionRepository) Get(ctx context.Context, token string) (*model.AdminSession, error) {
	const query = `
SELECT token, user_id, expires_at, last_seen_at, created_at
FROM admin_sessions
WHERE token = ?`

	var item model.AdminSession
	err := r.db.QueryRowContext(ctx, query, token).Scan(&item.Token, &item.UserID, &item.ExpiresAt, &item.LastSeenAt, &item.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get admin session: %w", err)
	}
	return &item, nil
}

func (r *AdminSessionRepository) Touch(ctx context.Context, token, lastSeenAt string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE admin_sessions SET last_seen_at = ? WHERE token = ?`, lastSeenAt, token)
	if err != nil {
		return fmt.Errorf("touch admin session: %w", err)
	}
	return ensureRowsAffected(result, "admin session not found")
}

func (r *AdminSessionRepository) Delete(ctx context.Context, token string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE token = ?`, token)
	if err != nil {
		return fmt.Errorf("delete admin session: %w", err)
	}
	return nil
}

func (r *AdminSessionRepository) DeleteExpired(ctx context.Context, now string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE expires_at <= ?`, now)
	if err != nil {
		return fmt.Errorf("delete expired admin sessions: %w", err)
	}
	return nil
}

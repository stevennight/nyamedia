package storage

import (
	"context"
	"database/sql"
	"fmt"

	"emby115/internal/model"
)

type AdminUserRepository struct {
	db *sql.DB
}

func NewAdminUserRepository(db *sql.DB) *AdminUserRepository {
	return &AdminUserRepository{db: db}
}

func (r *AdminUserRepository) GetByUsername(ctx context.Context, username string) (*model.AdminUser, error) {
	const query = `
SELECT id, username, password_hash, role, enabled, COALESCE(last_login_at, ''), created_at, updated_at
FROM admin_users
WHERE username = ?`

	item, err := scanAdminUserRow(r.db.QueryRowContext(ctx, query, username))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get admin user by username %s: %w", username, err)
	}
	return item, nil
}

func (r *AdminUserRepository) GetByID(ctx context.Context, id string) (*model.AdminUser, error) {
	const query = `
SELECT id, username, password_hash, role, enabled, COALESCE(last_login_at, ''), created_at, updated_at
FROM admin_users
WHERE id = ?`

	item, err := scanAdminUserRow(r.db.QueryRowContext(ctx, query, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get admin user by id %s: %w", id, err)
	}
	return item, nil
}

func (r *AdminUserRepository) Create(ctx context.Context, item model.AdminUser) error {
	const query = `
INSERT INTO admin_users (id, username, password_hash, role, enabled, last_login_at)
VALUES (?, ?, ?, ?, ?, NULLIF(?, ''))`

	_, err := r.db.ExecContext(ctx, query, item.ID, item.Username, item.PasswordHash, item.Role, boolToInt(item.Enabled), item.LastLoginAt)
	if err != nil {
		return fmt.Errorf("create admin user %s: %w", item.Username, err)
	}
	return nil
}

func (r *AdminUserRepository) UpdateCredentials(ctx context.Context, id, username, passwordHash string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE admin_users SET username = ?, password_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, username, passwordHash, id)
	if err != nil {
		return fmt.Errorf("update admin credentials %s: %w", id, err)
	}
	return ensureRowsAffected(result, "admin user not found")
}

func (r *AdminUserRepository) TouchLogin(ctx context.Context, id, loginAt string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE admin_users SET last_login_at = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, loginAt, id)
	if err != nil {
		return fmt.Errorf("touch admin login %s: %w", id, err)
	}
	return ensureRowsAffected(result, "admin user not found")
}

func scanAdminUserRow(scanner interface{ Scan(dest ...any) error }) (*model.AdminUser, error) {
	var item model.AdminUser
	var enabled int
	err := scanner.Scan(&item.ID, &item.Username, &item.PasswordHash, &item.Role, &enabled, &item.LastLoginAt, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	item.Enabled = enabled == 1
	return &item, nil
}

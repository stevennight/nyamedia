package storage

import (
	"database/sql"
	"embed"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func RunMigrations(db *sql.DB) error {
	if err := ensureSchemaMigrationsTable(db); err != nil {
		return err
	}

	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	filenames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		filenames = append(filenames, entry.Name())
	}
	sort.Strings(filenames)

	for _, name := range filenames {
		applied, err := migrationApplied(db, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		sqlBytes, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		if err := applyMigration(db, name, string(sqlBytes)); err != nil {
			return err
		}
	}

	return nil
}

func ensureSchemaMigrationsTable(db *sql.DB) error {
	const statement = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);`

	if _, err := db.Exec(statement); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

func migrationApplied(db *sql.DB, version string) (bool, error) {
	var exists int
	err := db.QueryRow("SELECT 1 FROM schema_migrations WHERE version = ? LIMIT 1", version).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query schema_migrations: %w", err)
	}
	return true, nil
}

func applyMigration(db *sql.DB, version, script string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", version, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.Exec(strings.TrimSpace(script)); err != nil {
		return fmt.Errorf("exec migration %s: %w", version, err)
	}
	if _, err = tx.Exec("INSERT INTO schema_migrations(version) VALUES (?)", version); err != nil {
		return fmt.Errorf("record migration %s: %w", version, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", version, err)
	}

	return nil
}

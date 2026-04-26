package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"NyaMedia/internal/config"
	"NyaMedia/internal/storage"

	_ "modernc.org/sqlite"
)

var tableOrder = []string{
	"admin_users",
	"providers",
	"libraries",
	"settings",
	"emby_servers",
	"admin_sessions",
	"provider_secrets",
	"library_mounts",
	"scan_tasks",
	"entries",
	"direct_link_cache",
	"provider_cache",
	"playback_logs",
	"task_logs",
	"system_events",
}

type tableResult struct {
	Table        string
	CopiedRows   int
	SourceExists bool
	TargetExists bool
	Columns      []string
	Skipped      bool
	Reason       string
}

func main() {
	var (
		configPath  = flag.String("config", "configs/bootstrap.yaml", "path to bootstrap config used to resolve postgres URL when -postgres-url is empty")
		postgresURL = flag.String("postgres-url", "", "target PostgreSQL database URL; overrides config")
		sqlitePath  = flag.String("sqlite-path", "", "path to source SQLite database file")
	)
	flag.Parse()

	if strings.TrimSpace(*sqlitePath) == "" {
		log.Fatal("sqlite-path is required")
	}

	resolvedPostgresURL, err := resolvePostgresURL(*configPath, *postgresURL)
	if err != nil {
		log.Fatalf("resolve postgres url: %v", err)
	}

	sqliteDB, err := sql.Open("sqlite", *sqlitePath)
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer sqliteDB.Close()

	sqliteDB.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	if err := sqliteDB.PingContext(ctx); err != nil {
		log.Fatalf("ping sqlite: %v", err)
	}

	postgresDB, err := storage.OpenPostgres(resolvedPostgresURL)
	if err != nil {
		log.Fatalf("open postgres: %v", err)
	}
	defer postgresDB.Close()

	if err := storage.RunMigrations(postgresDB); err != nil {
		log.Fatalf("run postgres migrations: %v", err)
	}

	results := make([]tableResult, 0, len(tableOrder))
	for _, table := range tableOrder {
		result, err := migrateTable(ctx, sqliteDB, postgresDB, table)
		if err != nil {
			log.Fatalf("migrate %s: %v", table, err)
		}
		results = append(results, result)
		logTableResult(result)
	}

	log.Println("migration summary:")
	for _, result := range results {
		logTableResult(result)
	}
}

func resolvePostgresURL(configPath, override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override), nil
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return "", err
	}
	return cfg.Storage.DatabaseURL, nil
}

func migrateTable(ctx context.Context, src, dst *sql.DB, table string) (tableResult, error) {
	result := tableResult{Table: table}

	sourceExists, err := sqliteTableExists(ctx, src, table)
	if err != nil {
		return result, err
	}
	result.SourceExists = sourceExists
	if !sourceExists {
		result.Skipped = true
		result.Reason = "source table not found"
		return result, nil
	}

	targetExists, err := postgresTableExists(ctx, dst, table)
	if err != nil {
		return result, err
	}
	result.TargetExists = targetExists
	if !targetExists {
		result.Skipped = true
		result.Reason = "target table not found"
		return result, nil
	}

	sourceColumns, err := sqliteColumns(ctx, src, table)
	if err != nil {
		return result, fmt.Errorf("read sqlite columns: %w", err)
	}
	targetColumns, err := postgresColumns(ctx, dst, table)
	if err != nil {
		return result, fmt.Errorf("read postgres columns: %w", err)
	}
	primaryKeys, err := postgresPrimaryKeys(ctx, dst, table)
	if err != nil {
		return result, fmt.Errorf("read postgres primary keys: %w", err)
	}

	commonColumns := intersectColumns(targetColumns, sourceColumns)
	result.Columns = commonColumns
	if len(commonColumns) == 0 {
		result.Skipped = true
		result.Reason = "no shared columns"
		return result, nil
	}

	selectSQL := buildSelectSQL(table, commonColumns)
	insertSQL := buildInsertSQL(table, commonColumns, primaryKeys)

	rows, err := src.QueryContext(ctx, selectSQL)
	if err != nil {
		return result, fmt.Errorf("select source rows: %w", err)
	}
	defer rows.Close()

	tx, err := dst.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin postgres transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return result, fmt.Errorf("prepare upsert statement: %w", err)
	}
	defer stmt.Close()

	values := make([]any, len(commonColumns))
	scanTargets := make([]any, len(commonColumns))
	for i := range values {
		scanTargets[i] = &values[i]
	}

	for rows.Next() {
		if err := rows.Scan(scanTargets...); err != nil {
			return result, fmt.Errorf("scan source row: %w", err)
		}
		args := make([]any, len(values))
		for i, value := range values {
			args[i] = normalizeValue(value)
		}
		if _, err := stmt.ExecContext(ctx, args...); err != nil {
			return result, fmt.Errorf("upsert row: %w", err)
		}
		result.CopiedRows++
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("iterate source rows: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit postgres transaction: %w", err)
	}

	return result, nil
}

func sqliteTableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var exists int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ? LIMIT 1`, table).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func postgresTableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var exists int
	err := db.QueryRowContext(ctx, `
SELECT 1
FROM information_schema.tables
WHERE table_schema = CURRENT_SCHEMA()
  AND table_name = ?
LIMIT 1`, table).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func sqliteColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, quoteSQLiteIdent(table)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make([]string, 0)
	for rows.Next() {
		var (
			cid        int
			name       string
			dataType   string
			notNull    int
			defaultVal any
			pk         int
		)
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultVal, &pk); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func postgresColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
SELECT column_name
FROM information_schema.columns
WHERE table_schema = CURRENT_SCHEMA()
  AND table_name = ?
ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func postgresPrimaryKeys(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
SELECT a.attname
FROM pg_index i
JOIN pg_class t ON t.oid = i.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
JOIN unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
WHERE n.nspname = CURRENT_SCHEMA()
  AND t.relname = ?
  AND i.indisprimary
ORDER BY k.ord`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		keys = append(keys, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

func intersectColumns(targetColumns, sourceColumns []string) []string {
	sourceSet := make(map[string]struct{}, len(sourceColumns))
	for _, name := range sourceColumns {
		sourceSet[name] = struct{}{}
	}

	common := make([]string, 0, len(targetColumns))
	for _, name := range targetColumns {
		if _, ok := sourceSet[name]; ok {
			common = append(common, name)
		}
	}
	return common
}

func buildSelectSQL(table string, columns []string) string {
	quoted := quoteIdentifiers(columns)
	return fmt.Sprintf(`SELECT %s FROM %s`, strings.Join(quoted, ", "), quoteSQLiteIdent(table))
}

func buildInsertSQL(table string, columns, primaryKeys []string) string {
	quotedColumns := quoteIdentifiers(columns)
	placeholders := strings.TrimRight(strings.Repeat("?, ", len(columns)), ", ")
	insertSQL := fmt.Sprintf(
		`INSERT INTO %s (%s) VALUES (%s)`,
		quoteIdent(table),
		strings.Join(quotedColumns, ", "),
		placeholders,
	)

	if len(primaryKeys) == 0 {
		return insertSQL
	}

	pkSet := make(map[string]struct{}, len(primaryKeys))
	for _, key := range primaryKeys {
		pkSet[key] = struct{}{}
	}

	updates := make([]string, 0, len(columns))
	for _, column := range columns {
		if _, isPrimaryKey := pkSet[column]; isPrimaryKey {
			continue
		}
		quoted := quoteIdent(column)
		updates = append(updates, fmt.Sprintf(`%s = EXCLUDED.%s`, quoted, quoted))
	}

	conflictColumns := strings.Join(quoteIdentifiers(primaryKeys), ", ")
	if len(updates) == 0 {
		return insertSQL + fmt.Sprintf(` ON CONFLICT (%s) DO NOTHING`, conflictColumns)
	}
	return insertSQL + fmt.Sprintf(` ON CONFLICT (%s) DO UPDATE SET %s`, conflictColumns, strings.Join(updates, ", "))
}

func quoteIdentifiers(columns []string) []string {
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, quoteIdent(column))
	}
	return quoted
}

func quoteIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteSQLiteIdent(value string) string {
	return quoteIdent(value)
}

func normalizeValue(value any) any {
	switch typed := value.(type) {
	case []byte:
		return string(typed)
	default:
		return typed
	}
}

func logTableResult(result tableResult) {
	if result.Skipped {
		log.Printf("table=%s skipped reason=%s", result.Table, result.Reason)
		return
	}

	orderedColumns := append([]string(nil), result.Columns...)
	sort.Strings(orderedColumns)
	log.Printf("table=%s copied_rows=%d columns=%s", result.Table, result.CopiedRows, strings.Join(orderedColumns, ","))
}

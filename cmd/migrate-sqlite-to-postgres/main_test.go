package main

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestBackfillLegacyScanSchedules(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	const schema = `
CREATE TABLE libraries (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL,
    scan_cron TEXT,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE scan_schedules (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    library_id TEXT NOT NULL,
    cron TEXT NOT NULL,
    enabled INTEGER NOT NULL
);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO libraries (id, name, enabled, scan_cron) VALUES
    ('anime', 'Anime', 1, '0 * * * *'),
    ('archive', 'Archive', 0, '0 4 * * *'),
    ('movies', 'Movies', 1, NULL)`); err != nil {
		t.Fatalf("seed libraries: %v", err)
	}

	inserted, err := backfillLegacyScanSchedules(context.Background(), db)
	if err != nil {
		t.Fatalf("backfillLegacyScanSchedules() error = %v", err)
	}
	if inserted != 2 {
		t.Fatalf("inserted = %d, want 2", inserted)
	}

	rows, err := db.Query(`SELECT library_id, cron, enabled FROM scan_schedules ORDER BY library_id`)
	if err != nil {
		t.Fatalf("list schedules: %v", err)
	}
	defer rows.Close()

	type schedule struct {
		libraryID string
		cron      string
		enabled   int
	}
	got := make([]schedule, 0, 2)
	for rows.Next() {
		var item schedule
		if err := rows.Scan(&item.libraryID, &item.cron, &item.enabled); err != nil {
			t.Fatalf("scan schedule: %v", err)
		}
		got = append(got, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate schedules: %v", err)
	}
	if len(got) != 2 || got[0] != (schedule{libraryID: "anime", cron: "0 * * * *", enabled: 1}) || got[1] != (schedule{libraryID: "archive", cron: "0 4 * * *", enabled: 0}) {
		t.Fatalf("schedules = %+v", got)
	}

	var remainingCron int
	if err := db.QueryRow(`SELECT COUNT(1) FROM libraries WHERE scan_cron IS NOT NULL`).Scan(&remainingCron); err != nil {
		t.Fatalf("count remaining cron values: %v", err)
	}
	if remainingCron != 0 {
		t.Fatalf("remaining cron values = %d, want 0", remainingCron)
	}

	inserted, err = backfillLegacyScanSchedules(context.Background(), db)
	if err != nil {
		t.Fatalf("second backfillLegacyScanSchedules() error = %v", err)
	}
	if inserted != 0 {
		t.Fatalf("second inserted = %d, want 0", inserted)
	}
}

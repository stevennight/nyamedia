package storage

import (
	"context"
	"database/sql"
	"testing"

	"NyaMedia/internal/model"

	_ "modernc.org/sqlite"
)

func TestScanScheduleRepositoryCRUDAndMarkRun(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	const schema = `
CREATE TABLE scan_schedules (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    library_id TEXT NOT NULL,
    mount_id TEXT,
    source_path TEXT,
    cron TEXT NOT NULL,
    enabled INTEGER NOT NULL,
    last_run_at TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	repository := NewScanScheduleRepository(db)
	ctx := context.Background()
	item := model.ScanSchedule{
		ID:         "active-series",
		Name:       "Active series",
		LibraryID:  "series",
		MountID:    "series-main",
		SourcePath: "/shows/active",
		Cron:       "*/15 * * * *",
		Enabled:    true,
	}
	if err := repository.Create(ctx, item); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	created, err := repository.Get(ctx, item.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if created == nil || created.Name != item.Name || created.SourcePath != item.SourcePath || !created.Enabled {
		t.Fatalf("Get() = %+v", created)
	}

	marked, err := repository.MarkRun(ctx, item.ID, "2026-07-18T12:30:00+08:00")
	if err != nil || !marked {
		t.Fatalf("first MarkRun() = %v, %v; want true, nil", marked, err)
	}
	marked, err = repository.MarkRun(ctx, item.ID, "2026-07-18T12:30:00+08:00")
	if err != nil || marked {
		t.Fatalf("duplicate MarkRun() = %v, %v; want false, nil", marked, err)
	}
	marked, err = repository.MarkRun(ctx, item.ID, "2026-07-18T12:31:00+08:00")
	if err != nil || !marked {
		t.Fatalf("next-minute MarkRun() = %v, %v; want true, nil", marked, err)
	}

	item.Name = "Paused active series"
	item.Cron = "0 * * * *"
	item.Enabled = false
	if err := repository.Update(ctx, item); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	enabled, err := repository.ListEnabled(ctx)
	if err != nil {
		t.Fatalf("ListEnabled() error = %v", err)
	}
	if len(enabled) != 0 {
		t.Fatalf("ListEnabled() returned %d items, want 0", len(enabled))
	}
	marked, err = repository.MarkRun(ctx, item.ID, "2026-07-18T13:00:00+08:00")
	if err != nil || marked {
		t.Fatalf("disabled MarkRun() = %v, %v; want false, nil", marked, err)
	}

	items, err := repository.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].Name != item.Name || items[0].Cron != item.Cron || items[0].LastRunAt == "" {
		t.Fatalf("List() = %+v", items)
	}

	if err := repository.Delete(ctx, item.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	deleted, err := repository.Get(ctx, item.ID)
	if err != nil || deleted != nil {
		t.Fatalf("Get() after delete = %+v, %v; want nil, nil", deleted, err)
	}
}

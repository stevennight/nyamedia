package storage

import (
	"context"
	"database/sql"
	"testing"

	"NyaMedia/internal/model"

	_ "modernc.org/sqlite"
)

func TestScanTaskRepositoryUpdateIfActivePreservesFirstTerminalState(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	const schema = `
CREATE TABLE scan_tasks (
    id TEXT PRIMARY KEY,
    task_type TEXT NOT NULL,
    library_id TEXT,
    status TEXT NOT NULL,
    progress_total INTEGER,
    progress_done INTEGER,
    message TEXT,
    error_message TEXT,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	repository := NewScanTaskRepository(db)
	ctx := context.Background()
	task := model.ScanTask{
		ID:        "task-race",
		TaskType:  "library_scan",
		LibraryID: "library-a",
		Status:    model.TaskStatusRunning,
		StartedAt: "2026-07-20T12:00:00Z",
	}
	if err := repository.Create(ctx, task); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	cancelled := task
	cancelled.Status = model.TaskStatusCancelled
	cancelled.Message = "scan cancelled"
	cancelled.FinishedAt = "2026-07-20T12:01:00Z"
	updated, err := repository.UpdateIfActive(ctx, cancelled)
	if err != nil || !updated {
		t.Fatalf("cancel UpdateIfActive() = %v, %v; want true, nil", updated, err)
	}

	completed := task
	completed.Status = model.TaskStatusCompleted
	completed.Message = "library scan completed"
	completed.FinishedAt = "2026-07-20T12:01:01Z"
	updated, err = repository.UpdateIfActive(ctx, completed)
	if err != nil || updated {
		t.Fatalf("late completion UpdateIfActive() = %v, %v; want false, nil", updated, err)
	}

	stored, err := repository.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored == nil || stored.Status != model.TaskStatusCancelled || stored.FinishedAt != cancelled.FinishedAt {
		t.Fatalf("Get() = %+v; want cancelled terminal state", stored)
	}
}

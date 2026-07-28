package storage

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestScanQueueRepositoryFirstDueExcludingProviders(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	const schema = `
CREATE TABLE scan_queue (
    id TEXT PRIMARY KEY,
    library_id TEXT NOT NULL,
    mount_id TEXT,
    provider_id TEXT NOT NULL,
    source_path TEXT NOT NULL,
    mode TEXT NOT NULL,
    source TEXT NOT NULL,
    run_after TEXT NOT NULL,
    status TEXT NOT NULL,
    event_count INTEGER NOT NULL,
    last_event_at TEXT NOT NULL,
    options_json TEXT,
    reason_json TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	const insert = `
INSERT INTO scan_queue (
    id, library_id, mount_id, provider_id, source_path, mode, source, run_after,
    status, event_count, last_event_at, created_at, updated_at
) VALUES (?, 'library-a', ?, ?, '/', 'recursive', 'manual', ?, 'pending', 1, ?, ?, ?)`
	for _, item := range []struct {
		id         string
		mountID    string
		providerID string
		runAfter   string
		createdAt  string
	}{
		{id: "queue-a", mountID: "mount-a", providerID: "provider-a", runAfter: "2026-07-20T10:00:00Z", createdAt: "2026-07-20T09:00:00Z"},
		{id: "queue-b", mountID: "mount-b", providerID: "provider-b", runAfter: "2026-07-20T10:00:01Z", createdAt: "2026-07-20T09:00:01Z"},
		{id: "queue-c", mountID: "mount-c", providerID: "provider-c", runAfter: "2026-07-20T12:00:00Z", createdAt: "2026-07-20T09:00:02Z"},
	} {
		if _, err := db.Exec(insert, item.id, item.mountID, item.providerID, item.runAfter, item.createdAt, item.createdAt, item.createdAt); err != nil {
			t.Fatalf("insert %s: %v", item.id, err)
		}
	}

	repository := NewScanQueueRepository(db)
	ctx := context.Background()
	const now = "2026-07-20T11:00:00Z"

	first, err := repository.FirstDue(ctx, now)
	if err != nil {
		t.Fatalf("FirstDue() error = %v", err)
	}
	if first == nil || first.ID != "queue-a" {
		t.Fatalf("FirstDue() = %+v; want queue-a", first)
	}

	first, err = repository.FirstDueExcludingProviders(ctx, now, []string{"provider-a"})
	if err != nil {
		t.Fatalf("FirstDueExcludingProviders() error = %v", err)
	}
	if first == nil || first.ID != "queue-b" {
		t.Fatalf("FirstDueExcludingProviders() = %+v; want queue-b", first)
	}

	first, err = repository.FirstDueExcludingProviders(ctx, now, []string{"provider-a", "provider-b"})
	if err != nil {
		t.Fatalf("FirstDueExcludingProviders(all due providers) error = %v", err)
	}
	if first != nil {
		t.Fatalf("FirstDueExcludingProviders(all due providers) = %+v; want nil", first)
	}
}

func TestScanQueueRecursiveCoversSamePathCurrentLevel(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	const schema = `
CREATE TABLE scan_queue (
    id TEXT PRIMARY KEY,
    library_id TEXT NOT NULL,
    mount_id TEXT,
    provider_id TEXT NOT NULL,
    source_path TEXT NOT NULL,
    mode TEXT NOT NULL,
    source TEXT NOT NULL,
    run_after TEXT NOT NULL,
    status TEXT NOT NULL,
    event_count INTEGER NOT NULL,
    last_event_at TEXT NOT NULL,
    options_json TEXT,
    reason_json TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	const insert = `
INSERT INTO scan_queue (
    id, library_id, mount_id, provider_id, source_path, mode, source, run_after,
    status, event_count, last_event_at, created_at, updated_at
) VALUES (?, 'library-a', 'mount-a', 'provider-a', ?, ?, 'webhook',
          '2026-07-28T00:00:00Z', 'pending', 1, '2026-07-28T00:00:00Z',
          '2026-07-28T00:00:00Z', '2026-07-28T00:00:00Z')`
	for _, item := range []struct {
		id         string
		sourcePath string
		mode       string
	}{
		{id: "recursive-same", sourcePath: "/Shows", mode: "recursive"},
		{id: "current-same", sourcePath: "/Shows", mode: "current_level"},
		{id: "current-child", sourcePath: "/Shows/Series", mode: "current_level"},
		{id: "recursive-other", sourcePath: "/Other", mode: "recursive"},
	} {
		if _, err := db.Exec(insert, item.id, item.sourcePath, item.mode); err != nil {
			t.Fatalf("insert %s: %v", item.id, err)
		}
	}

	repository := NewScanQueueRepository(db)
	ctx := context.Background()
	covering, err := repository.FindCoveringRecursive(ctx, "library-a", "mount-a", "provider-a", "/Shows")
	if err != nil {
		t.Fatal(err)
	}
	if covering == nil || covering.ID != "recursive-same" {
		t.Fatalf("covering recursive = %+v, want recursive-same", covering)
	}

	if err := repository.DeleteCovered(ctx, "library-a", "mount-a", "provider-a", "/Shows"); err != nil {
		t.Fatal(err)
	}
	items, err := repository.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	remaining := make(map[string]struct{}, len(items))
	for _, item := range items {
		remaining[item.ID] = struct{}{}
	}
	if _, ok := remaining["recursive-same"]; !ok {
		t.Fatal("same-path recursive item was deleted")
	}
	if _, ok := remaining["recursive-other"]; !ok {
		t.Fatal("unrelated recursive item was deleted")
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining queue items = %#v", remaining)
	}
}

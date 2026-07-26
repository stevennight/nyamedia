package storage

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestProviderCacheExpiration(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(`
CREATE TABLE provider_cache (
    provider_id TEXT NOT NULL,
    cache_key TEXT NOT NULL,
    cache_value TEXT NOT NULL,
    expire_at TEXT,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (provider_id, cache_key)
)`); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if _, err := db.Exec(`
INSERT INTO provider_cache (provider_id, cache_key, cache_value, expire_at)
VALUES (?, ?, ?, ?), (?, ?, ?, ?)`,
		"provider-a", "expired", "old", now.Add(-time.Minute).Format(time.RFC3339),
		"provider-a", "live", "new", now.Add(time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatal(err)
	}

	repository := NewProviderCacheRepository(db)
	if _, ok, err := repository.Get(context.Background(), "provider-a", "expired"); err != nil || ok {
		t.Fatalf("expired cache: ok=%v err=%v", ok, err)
	}
	if value, ok, err := repository.Get(context.Background(), "provider-a", "live"); err != nil || !ok || value != "new" {
		t.Fatalf("live cache: value=%q ok=%v err=%v", value, ok, err)
	}

	deleted, err := repository.DeleteExpired(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
}

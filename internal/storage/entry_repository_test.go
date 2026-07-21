package storage

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"NyaMedia/internal/model"

	_ "modernc.org/sqlite"
)

func TestEntryRepositoryListUnderPrefixEscapesLikeWildcards(t *testing.T) {
	repository, db := newEntryRepositoryTestDB(t)
	prefix := `/media/100%_done\raw`
	insertEntryFixtures(t, db,
		entryFixture{providerID: "provider-a", path: prefix, lastSeenAt: "current"},
		entryFixture{providerID: "provider-a", path: prefix + "/child", lastSeenAt: "current"},
		entryFixture{providerID: "provider-a", path: `/media/100ABXdone\raw/percent-and-underscore`, lastSeenAt: "current"},
		entryFixture{providerID: "provider-a", path: `/media/100%_doneraw/backslash`, lastSeenAt: "current"},
		entryFixture{providerID: "provider-b", path: prefix + "/other-provider", lastSeenAt: "current"},
	)

	items, err := repository.ListUnderPrefix(context.Background(), "provider-a", prefix)
	if err != nil {
		t.Fatalf("ListUnderPrefix() error = %v", err)
	}

	got := entryPaths(items)
	want := []string{prefix, prefix + "/child"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListUnderPrefix() paths = %q, want %q", got, want)
	}
}

func TestEntryRepositoryListPageEscapesLikeWildcards(t *testing.T) {
	repository, db := newEntryRepositoryTestDB(t)
	prefix := `/page/100%_done\raw`
	insertEntryFixtures(t, db,
		entryFixture{providerID: "provider-a", path: prefix, lastSeenAt: "current"},
		entryFixture{providerID: "provider-a", path: prefix + "/child", lastSeenAt: "current"},
		entryFixture{providerID: "provider-a", path: prefix + "-suffix", lastSeenAt: "current"},
		entryFixture{providerID: "provider-a", path: `/page/100ABXdone\raw/wildcards`, lastSeenAt: "current"},
		entryFixture{providerID: "provider-a", path: `/page/100%_doneraw/backslash`, lastSeenAt: "current"},
		entryFixture{providerID: "provider-b", path: prefix + "/other-provider", lastSeenAt: "current"},
	)

	items, total, err := repository.ListPage(context.Background(), "provider-a", prefix, 20, 0)
	if err != nil {
		t.Fatalf("ListPage() error = %v", err)
	}

	got := entryPaths(items)
	want := []string{prefix, prefix + "-suffix", prefix + "/child"}
	sort.Strings(want)
	if total != len(want) {
		t.Fatalf("ListPage() total = %d, want %d", total, len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListPage() paths = %q, want %q", got, want)
	}
}

func TestEntryRepositoryListCursorUsesStableCompositeCursor(t *testing.T) {
	repository, db := newEntryRepositoryTestDB(t)
	insertEntryFixtures(t, db,
		entryFixture{providerID: "provider-a", path: "/alpha", lastSeenAt: "current", updatedAt: "2026-07-20 12:00:03"},
		entryFixture{providerID: "provider-a", path: "/beta", lastSeenAt: "current", updatedAt: "2026-07-20 12:00:03"},
		entryFixture{providerID: "provider-b", path: "/alpha", lastSeenAt: "current", updatedAt: "2026-07-20 12:00:03"},
		entryFixture{providerID: "provider-a", path: "/older", lastSeenAt: "current", updatedAt: "2026-07-20 12:00:02"},
		entryFixture{providerID: "provider-c", path: "/oldest", lastSeenAt: "current", updatedAt: "2026-07-20 12:00:01"},
	)

	first, hasMore, err := repository.ListCursor(context.Background(), EntryListOptions{Limit: 2})
	if err != nil {
		t.Fatalf("ListCursor() first page error = %v", err)
	}
	if !hasMore {
		t.Fatal("ListCursor() first page hasMore = false, want true")
	}
	if got, want := entryKeys(first), []string{"2026-07-20 12:00:03|provider-a|/alpha", "2026-07-20 12:00:03|provider-a|/beta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListCursor() first page = %q, want %q", got, want)
	}

	second, hasMore, err := repository.ListCursor(context.Background(), EntryListOptions{
		Limit:            2,
		CursorUpdatedAt:  first[len(first)-1].UpdatedAt,
		CursorProviderID: first[len(first)-1].ProviderID,
		CursorPath:       first[len(first)-1].Path,
	})
	if err != nil {
		t.Fatalf("ListCursor() second page error = %v", err)
	}
	if !hasMore {
		t.Fatal("ListCursor() second page hasMore = false, want true")
	}
	if got, want := entryKeys(second), []string{"2026-07-20 12:00:03|provider-b|/alpha", "2026-07-20 12:00:02|provider-a|/older"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListCursor() second page = %q, want %q", got, want)
	}

	third, hasMore, err := repository.ListCursor(context.Background(), EntryListOptions{
		Limit:            2,
		CursorUpdatedAt:  second[len(second)-1].UpdatedAt,
		CursorProviderID: second[len(second)-1].ProviderID,
		CursorPath:       second[len(second)-1].Path,
	})
	if err != nil {
		t.Fatalf("ListCursor() third page error = %v", err)
	}
	if hasMore {
		t.Fatal("ListCursor() third page hasMore = true, want false")
	}
	if got, want := entryKeys(third), []string{"2026-07-20 12:00:01|provider-c|/oldest"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListCursor() third page = %q, want %q", got, want)
	}
}

func TestEntryRepositoryListCursorFiltersAndEscapesLikeWildcards(t *testing.T) {
	repository, db := newEntryRepositoryTestDB(t)
	prefix := `/cursor/100%_done\raw`
	insertEntryFixtures(t, db,
		entryFixture{providerID: "provider-a", path: prefix + "/new", lastSeenAt: "current", updatedAt: "2026-07-20 12:00:02"},
		entryFixture{providerID: "provider-a", path: prefix + "/old", lastSeenAt: "current", updatedAt: "2026-07-20 12:00:01"},
		entryFixture{providerID: "provider-a", path: `/cursor/100ABXdone\raw/wildcards`, lastSeenAt: "current", updatedAt: "2026-07-20 12:00:03"},
		entryFixture{providerID: "provider-b", path: prefix + "/other-provider", lastSeenAt: "current", updatedAt: "2026-07-20 12:00:03"},
	)

	items, hasMore, err := repository.ListCursor(context.Background(), EntryListOptions{
		ProviderID: "provider-a",
		Prefix:     prefix,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListCursor() error = %v", err)
	}
	if hasMore {
		t.Fatal("ListCursor() hasMore = true, want false")
	}
	if got, want := entryPaths(items), []string{prefix + "/new", prefix + "/old"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListCursor() paths = %q, want %q", got, want)
	}
}

func TestEntryRepositoryListCursorRejectsPartialCursor(t *testing.T) {
	repository, _ := newEntryRepositoryTestDB(t)

	_, _, err := repository.ListCursor(context.Background(), EntryListOptions{CursorUpdatedAt: "2026-07-20 12:00:03"})
	if err == nil {
		t.Fatal("ListCursor() error = nil, want partial cursor error")
	}
}

func TestEntryRepositoryDeleteUnderPrefixEscapesLikeWildcards(t *testing.T) {
	repository, db := newEntryRepositoryTestDB(t)
	prefix := `/delete/100%_done\raw`
	insertEntryFixtures(t, db,
		entryFixture{providerID: "provider-a", path: prefix, lastSeenAt: "current"},
		entryFixture{providerID: "provider-a", path: prefix + "/child", lastSeenAt: "current"},
		entryFixture{providerID: "provider-a", path: prefix + "-sibling", lastSeenAt: "current"},
		entryFixture{providerID: "provider-a", path: `/delete/100ABXdone\raw/wildcards`, lastSeenAt: "current"},
		entryFixture{providerID: "provider-a", path: `/delete/100%_doneraw/backslash`, lastSeenAt: "current"},
		entryFixture{providerID: "provider-b", path: prefix + "/other-provider", lastSeenAt: "current"},
	)

	if err := repository.DeleteUnderPrefix(context.Background(), "provider-a", prefix); err != nil {
		t.Fatalf("DeleteUnderPrefix() error = %v", err)
	}

	got := storedPaths(t, db, "provider-a")
	want := []string{
		`/delete/100%_doneraw/backslash`,
		prefix + "-sibling",
		`/delete/100ABXdone\raw/wildcards`,
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths after DeleteUnderPrefix() = %q, want %q", got, want)
	}
	if gotOther := storedPaths(t, db, "provider-b"); !reflect.DeepEqual(gotOther, []string{prefix + "/other-provider"}) {
		t.Fatalf("other provider paths after DeleteUnderPrefix() = %q", gotOther)
	}
}

func TestEntryRepositoryDeleteStaleUnderPrefixEscapesLikeWildcards(t *testing.T) {
	repository, db := newEntryRepositoryTestDB(t)
	prefix := `/stale/100%_done\raw`
	insertEntryFixtures(t, db,
		entryFixture{providerID: "provider-a", path: prefix, lastSeenAt: "old"},
		entryFixture{providerID: "provider-a", path: prefix + "/old-child", lastSeenAt: "old"},
		entryFixture{providerID: "provider-a", path: prefix + "/fresh-child", lastSeenAt: "current"},
		entryFixture{providerID: "provider-a", path: prefix + "-sibling", lastSeenAt: "old"},
		entryFixture{providerID: "provider-a", path: `/stale/100ABXdone\raw/wildcards`, lastSeenAt: "old"},
		entryFixture{providerID: "provider-a", path: `/stale/100%_doneraw/backslash`, lastSeenAt: "old"},
		entryFixture{providerID: "provider-b", path: prefix + "/other-provider", lastSeenAt: "old"},
	)

	if err := repository.DeleteStaleUnderPrefix(context.Background(), "provider-a", prefix, "current"); err != nil {
		t.Fatalf("DeleteStaleUnderPrefix() error = %v", err)
	}

	got := storedPaths(t, db, "provider-a")
	want := []string{
		`/stale/100%_doneraw/backslash`,
		prefix + "-sibling",
		prefix + "/fresh-child",
		`/stale/100ABXdone\raw/wildcards`,
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths after DeleteStaleUnderPrefix() = %q, want %q", got, want)
	}
	if gotOther := storedPaths(t, db, "provider-b"); !reflect.DeepEqual(gotOther, []string{prefix + "/other-provider"}) {
		t.Fatalf("other provider paths after DeleteStaleUnderPrefix() = %q", gotOther)
	}
}

type entryFixture struct {
	providerID string
	path       string
	lastSeenAt string
	updatedAt  string
}

func newEntryRepositoryTestDB(t *testing.T) (*EntryRepository, *sql.DB) {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	const schema = `
CREATE TABLE entries (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    entry_type TEXT NOT NULL,
    path TEXT NOT NULL,
    parent_path TEXT,
    name TEXT NOT NULL,
    size BIGINT,
    mtime TEXT,
    mime_type TEXT,
    content_hash TEXT,
    provider_entry_id TEXT,
    metadata_json TEXT,
    last_seen_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create entries schema: %v", err)
	}

	return NewEntryRepository(db), db
}

func insertEntryFixtures(t *testing.T, db *sql.DB, fixtures ...entryFixture) {
	t.Helper()

	const query = `
INSERT INTO entries (id, provider_id, entry_type, path, name, last_seen_at, updated_at)
VALUES (?, ?, 'file', ?, ?, ?, ?)`
	for index, fixture := range fixtures {
		updatedAt := fixture.updatedAt
		if updatedAt == "" {
			updatedAt = "2026-07-20 12:00:00"
		}
		if _, err := db.Exec(query, fmt.Sprintf("entry-%d", index), fixture.providerID, fixture.path, fixture.path, fixture.lastSeenAt, updatedAt); err != nil {
			t.Fatalf("insert fixture %q: %v", fixture.path, err)
		}
	}
}

func storedPaths(t *testing.T, db *sql.DB, providerID string) []string {
	t.Helper()

	rows, err := db.Query(`SELECT path FROM entries WHERE provider_id = ? ORDER BY path`, providerID)
	if err != nil {
		t.Fatalf("query stored paths: %v", err)
	}
	defer rows.Close()

	paths := make([]string, 0)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			t.Fatalf("scan stored path: %v", err)
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate stored paths: %v", err)
	}
	return paths
}

func entryPaths(items []model.Entry) []string {
	paths := make([]string, 0, len(items))
	for _, item := range items {
		paths = append(paths, item.Path)
	}
	sort.Strings(paths)
	return paths
}

func entryKeys(items []model.Entry) []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.UpdatedAt+"|"+item.ProviderID+"|"+item.Path)
	}
	return keys
}

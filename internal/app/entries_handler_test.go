package app

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"NyaMedia/internal/model"
	"NyaMedia/internal/storage"

	_ "modernc.org/sqlite"
)

func TestHandleEntriesCursorPagination(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
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
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	const insert = `
INSERT INTO entries (id, provider_id, entry_type, path, name, last_seen_at, created_at, updated_at)
VALUES (?, 'provider-a', 'file', ?, ?, '2026-07-20 12:00:00', '2026-07-20 12:00:00', ?)`
	for _, item := range []struct{ id, path, updatedAt string }{
		{"entry-1", "/one", "2026-07-20 12:00:03"},
		{"entry-2", "/two", "2026-07-20 12:00:02"},
		{"entry-3", "/three", "2026-07-20 12:00:01"},
	} {
		if _, err := db.Exec(insert, item.id, item.path, item.path, item.updatedAt); err != nil {
			t.Fatal(err)
		}
	}

	a := &App{entries: storage.NewEntryRepository(db)}
	first := requestEntriesPage(t, a, url.Values{"pagination": {"cursor"}, "limit": {"2"}})
	if len(first.Items) != 2 || !first.Pagination.HasMore || first.Pagination.NextCursor == nil {
		t.Fatalf("first page = %+v", first)
	}

	second := requestEntriesPage(t, a, url.Values{
		"pagination":         {"cursor"},
		"limit":              {"2"},
		"cursor_updated_at":  {first.Pagination.NextCursor.UpdatedAt},
		"cursor_provider_id": {first.Pagination.NextCursor.ProviderID},
		"cursor_path":        {first.Pagination.NextCursor.Path},
	})
	if len(second.Items) != 1 || second.Items[0].ID != "entry-3" || second.Pagination.HasMore {
		t.Fatalf("second page = %+v", second)
	}
}

type entriesCursorResponse struct {
	Items      []model.Entry `json:"items"`
	Pagination struct {
		HasMore    bool `json:"has_more"`
		NextCursor *struct {
			UpdatedAt  string `json:"updated_at"`
			ProviderID string `json:"provider_id"`
			Path       string `json:"path"`
		} `json:"next_cursor"`
	} `json:"pagination"`
}

func requestEntriesPage(t *testing.T, a *App, query url.Values) entriesCursorResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/entries?"+query.Encode(), nil)
	a.handleEntries(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response entriesCursorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

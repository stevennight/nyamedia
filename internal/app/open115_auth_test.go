package app

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"NyaMedia/internal/model"
	"NyaMedia/internal/storage"

	_ "modernc.org/sqlite"
)

func TestHandleProvider115OpenTokenImport(t *testing.T) {
	app, db := newOpen115TokenImportTestApp(t)
	if _, err := db.Exec(`
INSERT INTO provider_cache (provider_id, cache_key, cache_value)
VALUES ('provider-a', 'children:/', '{}')`); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/providers/provider-a/auth/115open", strings.NewReader(`{
		"client_id": " client-a ",
		"access_token": " access-a ",
		"refresh_token": " refresh-a "
	}`))
	app.handleProvider115OpenTokenImport(recorder, request, model.Provider{ID: "provider-a", Type: "115open"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		ProviderID string   `json:"provider_id"`
		Saved      []string `json:"saved"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ProviderID != "provider-a" || strings.Join(response.Saved, ",") != "client_id,access_token,refresh_token" {
		t.Fatalf("response = %+v", response)
	}

	secrets, err := app.loadProviderSecretValues(request.Context(), "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if secrets["client_id"] != "client-a" || secrets["access_token"] != "access-a" || secrets["refresh_token"] != "refresh-a" {
		t.Fatalf("secrets = %+v", secrets)
	}

	var cacheCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_cache WHERE provider_id = 'provider-a'`).Scan(&cacheCount); err != nil {
		t.Fatal(err)
	}
	if cacheCount != 0 {
		t.Fatalf("provider cache rows = %d, want 0", cacheCount)
	}
}

func TestHandleProvider115OpenTokenImportRequiresToken(t *testing.T) {
	app, _ := newOpen115TokenImportTestApp(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/providers/provider-a/auth/115open", strings.NewReader(`{"client_id":"client-a"}`))

	app.handleProvider115OpenTokenImport(recorder, request, model.Provider{ID: "provider-a", Type: "115open"})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "access_token or refresh_token is required") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func newOpen115TokenImportTestApp(t *testing.T) (*App, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	const schema = `
CREATE TABLE provider_secrets (
    provider_id TEXT NOT NULL,
    secret_type TEXT NOT NULL,
    secret_value TEXT NOT NULL,
    masked_value TEXT,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (provider_id, secret_type)
);
CREATE TABLE provider_cache (
    provider_id TEXT NOT NULL,
    cache_key TEXT NOT NULL,
    cache_value TEXT NOT NULL,
    expire_at TEXT,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (provider_id, cache_key)
);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}

	return &App{
		secrets:       storage.NewProviderSecretRepository(db),
		providerCache: storage.NewProviderCacheRepository(db),
	}, db
}

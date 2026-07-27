package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"NyaMedia/internal/model"
)

func TestHandleProvider123PanCredentials(t *testing.T) {
	app, db := newOpen115TokenImportTestApp(t)
	if _, err := db.Exec(`
INSERT INTO provider_cache (provider_id, cache_key, cache_value)
VALUES ('provider-a', 'children:/', '{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO direct_link_cache (provider_id) VALUES ('provider-a')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO entries (provider_id) VALUES ('provider-a')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO provider_secrets (provider_id, secret_type, secret_value, masked_value)
VALUES
    ('provider-a', 'access_token', 'old-access', 'ol******ss'),
    ('provider-a', 'access_token_expires_at', '2099-01-01T00:00:00Z', '20******************0Z')`); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/providers/provider-a/auth/123pan", strings.NewReader(`{
		"client_id": " client-a ",
		"client_secret": " secret-a "
	}`))
	app.handleProvider123PanCredentials(recorder, request, model.Provider{ID: "provider-a", Type: "123pan"})
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
	if response.ProviderID != "provider-a" || strings.Join(response.Saved, ",") != "client_id,client_secret" {
		t.Fatalf("response = %+v", response)
	}

	secrets, err := app.loadProviderSecretValues(request.Context(), "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if secrets["client_id"] != "client-a" || secrets["client_secret"] != "secret-a" {
		t.Fatalf("secrets = %+v", secrets)
	}
	if _, ok := secrets["access_token"]; ok {
		t.Fatalf("stale access token was retained: %+v", secrets)
	}
	if _, ok := secrets["access_token_expires_at"]; ok {
		t.Fatalf("stale access token expiry was retained: %+v", secrets)
	}

	var cacheCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_cache WHERE provider_id = 'provider-a'`).Scan(&cacheCount); err != nil {
		t.Fatal(err)
	}
	if cacheCount != 0 {
		t.Fatalf("provider cache rows = %d, want 0", cacheCount)
	}
	for _, table := range []string{"direct_link_cache", "entries"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE provider_id = 'provider-a'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s rows = %d, want 0", table, count)
		}
	}

	secretsRecorder := httptest.NewRecorder()
	secretsRequest := httptest.NewRequest(http.MethodGet, "/api/v1/providers/provider-a/secrets", nil)
	app.handleProviderSecrets(secretsRecorder, secretsRequest, "provider-a")
	if secretsRecorder.Code != http.StatusOK {
		t.Fatalf("list secrets status = %d, body = %s", secretsRecorder.Code, secretsRecorder.Body.String())
	}
	if body := secretsRecorder.Body.String(); strings.Contains(body, "client-a") || strings.Contains(body, "secret-a") || strings.Contains(body, "old-access") {
		t.Fatalf("secret response contains a raw credential: %s", body)
	}
}

func TestHandleProvider123PanCredentialsRequiresBothValues(t *testing.T) {
	app, _ := newOpen115TokenImportTestApp(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/providers/provider-a/auth/123pan", strings.NewReader(`{"client_id":"client-a"}`))

	app.handleProvider123PanCredentials(recorder, request, model.Provider{ID: "provider-a", Type: "123pan"})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "client_id and client_secret are required") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestBuildProvider123PanUsesStoredCredentials(t *testing.T) {
	app, db := newOpen115TokenImportTestApp(t)
	if _, err := db.Exec(`
INSERT INTO provider_secrets (provider_id, secret_type, secret_value, masked_value)
VALUES
    ('provider-a', 'client_id', 'client-a', 'cl****-a'),
    ('provider-a', 'client_secret', 'secret-a', 'se****-a'),
    ('provider-a', 'access_token', 'access-a', 'ac****-a'),
    ('provider-a', 'access_token_expires_at', '2099-01-01T00:00:00Z', '20******************0Z')`); err != nil {
		t.Fatal(err)
	}

	runtimeProvider, ok, err := app.buildProvider(model.Provider{
		ID:       "provider-a",
		Type:     "123pan",
		RootPath: "/Movies",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || runtimeProvider == nil {
		t.Fatal("123pan provider was not constructed")
	}
	if runtimeProvider.ID() != "provider-a" || runtimeProvider.Type() != "123pan" {
		t.Fatalf("provider id=%q type=%q", runtimeProvider.ID(), runtimeProvider.Type())
	}

	secondProvider, ok, err := app.buildProvider(model.Provider{
		ID:       "provider-a",
		Type:     "123pan",
		RootPath: "/Movies",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || secondProvider != runtimeProvider {
		t.Fatal("123pan runtime was not reused across buildProvider calls")
	}

	app.invalidatePan123Provider("provider-a")
	thirdProvider, ok, err := app.buildProvider(model.Provider{
		ID:       "provider-a",
		Type:     "123pan",
		RootPath: "/Movies",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || thirdProvider == runtimeProvider {
		t.Fatal("invalidated 123pan runtime was reused")
	}
}

func TestPersistProvider123PanTokenReplacesTokenPair(t *testing.T) {
	app, db := newOpen115TokenImportTestApp(t)
	if _, err := db.Exec(`
INSERT INTO provider_secrets (provider_id, secret_type, secret_value, masked_value)
VALUES
    ('provider-a', 'client_id', 'client-a', 'cl****-a'),
    ('provider-a', 'client_secret', 'secret-a', 'se****-a'),
    ('provider-a', 'access_token', 'old-access', 'ol****ss'),
    ('provider-a', 'access_token_expires_at', '2000-01-01T00:00:00Z', '20******************0Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO provider_cache (provider_id, cache_key, cache_value)
VALUES ('provider-a', 'children:/', '{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO direct_link_cache (provider_id) VALUES ('provider-a')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO entries (provider_id) VALUES ('provider-a')`); err != nil {
		t.Fatal(err)
	}

	app.persistProvider123PanToken("provider-a", "client-a", "secret-a", "new-access", "2099-01-01T00:00:00Z")
	secrets, err := app.loadProviderSecretValues(context.Background(), "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if secrets["access_token"] != "new-access" || secrets["access_token_expires_at"] != "2099-01-01T00:00:00Z" {
		t.Fatalf("secrets = %+v", secrets)
	}

	app.persistProvider123PanToken("provider-a", "client-a", "secret-a", "token-without-expiry", "")
	secrets, err = app.loadProviderSecretValues(context.Background(), "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if secrets["access_token"] != "token-without-expiry" {
		t.Fatalf("access token = %q", secrets["access_token"])
	}
	if _, exists := secrets["access_token_expires_at"]; exists {
		t.Fatalf("stale expiry was retained: %+v", secrets)
	}
	app.persistProvider123PanToken("provider-a", "old-client", "old-secret", "late-old-token", "2099-02-01T00:00:00Z")
	secrets, err = app.loadProviderSecretValues(context.Background(), "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if secrets["access_token"] != "token-without-expiry" {
		t.Fatalf("stale credential callback replaced token: %+v", secrets)
	}
	for _, table := range []string{"provider_cache", "direct_link_cache", "entries"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE provider_id = 'provider-a'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s rows = %d, want 1 after token refresh", table, count)
		}
	}
}

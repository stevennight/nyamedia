package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"NyaMedia/internal/model"
)

func TestHandleProviderBaiduOpenCredentialsClearsTokensAndProviderState(t *testing.T) {
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
    ('provider-a', 'refresh_token', 'old-refresh', 'ol*******sh'),
    ('provider-a', 'access_token_expires_at', '2099-01-01T00:00:00Z', '20******************0Z')`); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/providers/provider-a/auth/baiduopen", strings.NewReader(`{
		"client_id": " client-a ",
		"client_secret": " secret-a "
	}`))
	app.handleProviderBaiduOpenCredentials(recorder, request, model.Provider{ID: "provider-a", Type: "baiduopen"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	secrets, err := app.loadProviderSecretValues(request.Context(), "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if secrets["client_id"] != "client-a" || secrets["client_secret"] != "secret-a" {
		t.Fatalf("secrets = %+v", secrets)
	}
	for _, key := range []string{"access_token", "refresh_token", "access_token_expires_at"} {
		if _, ok := secrets[key]; ok {
			t.Fatalf("stale %s was retained: %+v", key, secrets)
		}
	}
	for _, table := range []string{"provider_cache", "direct_link_cache", "entries"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE provider_id = 'provider-a'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s rows = %d, want 0", table, count)
		}
	}
}

func TestHandleProviderBaiduOpenAuthStartBuildsOfficialURL(t *testing.T) {
	app, db := newOpen115TokenImportTestApp(t)
	app.baiduAuthFlows = make(map[string]*baiduOpenAuthFlow)
	if _, err := db.Exec(`
INSERT INTO provider_secrets (provider_id, secret_type, secret_value, masked_value)
VALUES
    ('provider-a', 'client_id', 'client-a', 'cl****-a'),
    ('provider-a', 'client_secret', 'secret-a', 'se****-a')`); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/providers/provider-a/auth/baiduopen", strings.NewReader(`{
		"redirect_uri": "https://nya.example/api/v1/providers/provider-a/auth/baiduopen/callback"
	}`))
	app.handleProviderBaiduOpenAuthStart(recorder, request, model.Provider{ID: "provider-a", Type: "baiduopen"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var response baiduOpenAuthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SessionID == "" || response.State != "pending" {
		t.Fatalf("response = %+v", response)
	}
	parsed, err := url.Parse(response.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Host != "openapi.baidu.com" ||
		query.Get("client_id") != "client-a" ||
		query.Get("scope") != "basic,netdisk" ||
		query.Get("state") != response.SessionID ||
		query.Get("redirect_uri") != response.RedirectURI {
		t.Fatalf("authorization url = %s", response.AuthorizationURL)
	}
}

func TestValidateBaiduOpenRedirectURI(t *testing.T) {
	for _, value := range []string{"", "relative/callback", "ftp://example.com/callback", "https://user@example.com/callback", "https://example.com/callback#fragment"} {
		if _, err := validateBaiduOpenRedirectURI(value); err == nil {
			t.Fatalf("validateBaiduOpenRedirectURI(%q) succeeded", value)
		}
	}
	got, err := validateBaiduOpenRedirectURI("https://nya.example/callback")
	if err != nil || got != "https://nya.example/callback" {
		t.Fatalf("got %q, err=%v", got, err)
	}
}

func TestBuildProviderBaiduOpenUsesStoredCredentials(t *testing.T) {
	app, db := newOpen115TokenImportTestApp(t)
	if _, err := db.Exec(`
INSERT INTO provider_secrets (provider_id, secret_type, secret_value, masked_value)
VALUES
    ('provider-a', 'client_id', 'client-a', 'cl****-a'),
    ('provider-a', 'client_secret', 'secret-a', 'se****-a'),
    ('provider-a', 'access_token', 'access-a', 'ac****-a'),
    ('provider-a', 'refresh_token', 'refresh-a', 're*****-a'),
    ('provider-a', 'access_token_expires_at', '2099-01-01T00:00:00Z', '20******************0Z')`); err != nil {
		t.Fatal(err)
	}

	providerModel := model.Provider{ID: "provider-a", Type: "baiduopen", RootPath: "/Movies"}
	runtimeProvider, ok, err := app.buildProvider(providerModel)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || runtimeProvider == nil || runtimeProvider.Type() != "baiduopen" {
		t.Fatalf("provider = %#v ok=%t", runtimeProvider, ok)
	}
	second, ok, err := app.buildProvider(providerModel)
	if err != nil || !ok || second != runtimeProvider {
		t.Fatalf("cached provider = %#v ok=%t err=%v", second, ok, err)
	}
	app.invalidateBaiduOpenProvider(providerModel.ID)
	third, ok, err := app.buildProvider(providerModel)
	if err != nil || !ok || third == runtimeProvider {
		t.Fatalf("invalidated provider = %#v ok=%t err=%v", third, ok, err)
	}
}

func TestPersistProviderBaiduOpenTokensRejectsChangedCredentials(t *testing.T) {
	app, db := newOpen115TokenImportTestApp(t)
	if _, err := db.Exec(`
INSERT INTO provider_secrets (provider_id, secret_type, secret_value, masked_value)
VALUES
    ('provider-a', 'client_id', 'client-a', 'cl****-a'),
    ('provider-a', 'client_secret', 'secret-a', 'se****-a')`); err != nil {
		t.Fatal(err)
	}

	if !app.persistProviderBaiduOpenTokens(
		"provider-a",
		"client-a",
		"secret-a",
		"access-a",
		"refresh-a",
		"2099-01-01T00:00:00Z",
	) {
		t.Fatal("token persistence failed")
	}
	if app.persistProviderBaiduOpenTokens(
		"provider-a",
		"old-client",
		"old-secret",
		"late-access",
		"late-refresh",
		"2099-02-01T00:00:00Z",
	) {
		t.Fatal("stale credential callback persisted tokens")
	}
	secrets, err := app.loadProviderSecretValues(context.Background(), "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if secrets["access_token"] != "access-a" || secrets["refresh_token"] != "refresh-a" {
		t.Fatalf("secrets = %+v", secrets)
	}
}

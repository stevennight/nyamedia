package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"NyaMedia/internal/config"
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

func TestHandleProviderBaiduOpenCredentialsKeepsSavedSecretAndTokensWhenUnchanged(t *testing.T) {
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

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/providers/provider-a/auth/baiduopen", strings.NewReader(`{
		"client_id": "client-a",
		"client_secret": ""
	}`))
	providerModel := model.Provider{ID: "provider-a", Type: "baiduopen", ConfigJSON: `{"baiduopen_auth_mode":"broker_relay"}`}
	app.handleProviderBaiduOpenCredentials(recorder, request, providerModel)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var response baiduOpenAuthConfigResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ClientID != "client-a" || !response.ClientSecretConfigured || !response.AccessTokenConfigured || !response.RefreshTokenConfigured || response.AuthMode != baiduOpenAuthModeBrokerRelay {
		t.Fatalf("response = %+v", response)
	}
	secrets, err := app.loadProviderSecretValues(request.Context(), "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if secrets["client_secret"] != "secret-a" || secrets["access_token"] != "access-a" || secrets["refresh_token"] != "refresh-a" {
		t.Fatalf("secrets = %+v", secrets)
	}
}

func TestHandleProviderBaiduOpenAuthConfigReportsSavedFieldsWithoutSecrets(t *testing.T) {
	app, db := newOpen115TokenImportTestApp(t)
	if _, err := db.Exec(`
INSERT INTO providers (id, type, name, root_path, config_json)
VALUES ('provider-a', 'baiduopen', 'Baidu', '/', '{"baiduopen_auth_mode":"broker_token_exchange"}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO provider_secrets (provider_id, secret_type, secret_value, masked_value)
VALUES
    ('provider-a', 'client_id', 'visible-client-id', 'vi************id'),
    ('provider-a', 'client_secret', 'hidden-client-secret', 'hi****************et'),
    ('provider-a', 'access_token', 'hidden-access-token', 'hi***************en'),
    ('provider-a', 'refresh_token', 'hidden-refresh-token', 'hi****************en')`); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/providers/provider-a/auth/baiduopen", nil)
	app.handleProviderBaiduOpenAuth(recorder, request, "provider-a")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response baiduOpenAuthConfigResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ClientID != "visible-client-id" || !response.ClientSecretConfigured || !response.AccessTokenConfigured || !response.RefreshTokenConfigured || response.AuthMode != baiduOpenAuthModeTokenExchange {
		t.Fatalf("response = %+v", response)
	}
	for _, secret := range []string{"hidden-client-secret", "hidden-access-token", "hidden-refresh-token"} {
		if strings.Contains(recorder.Body.String(), secret) {
			t.Fatalf("response exposed secret %q: %s", secret, recorder.Body.String())
		}
	}
}

func TestHandleProviderBaiduOpenAuthModePreservesOtherConfig(t *testing.T) {
	app, db := newOpen115TokenImportTestApp(t)
	if _, err := db.Exec(`
INSERT INTO providers (id, type, name, root_path, config_json)
VALUES ('provider-a', 'baiduopen', 'Baidu', '/', '{"scan_request_interval_ms":750}')`); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/providers/provider-a/auth/baiduopen/mode", strings.NewReader(`{"mode":"broker_relay"}`))
	app.handleProviderBaiduOpenAuthMode(recorder, request, "provider-a")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	providerModel, err := app.providers.Get(request.Context(), "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(providerModel.ConfigJSON), &config); err != nil {
		t.Fatal(err)
	}
	if config["baiduopen_auth_mode"] != baiduOpenAuthModeBrokerRelay || config["scan_request_interval_ms"] != float64(750) {
		t.Fatalf("config = %+v", config)
	}
}

func TestHandleProviderBaiduOpenAuthStartBuildsOfficialURL(t *testing.T) {
	app, db := newOpen115TokenImportTestApp(t)
	app.config = config.Config{Server: config.ServerConfig{PublicBaseURL: "https://nya.example/root"}}
	app.baiduAuthFlows = make(map[string]*baiduOpenAuthFlow)
	if _, err := db.Exec(`
INSERT INTO provider_secrets (provider_id, secret_type, secret_value, masked_value)
VALUES
    ('provider-a', 'client_id', 'client-a', 'cl****-a'),
    ('provider-a', 'client_secret', 'secret-a', 'se****-a')`); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/providers/provider-a/auth/baiduopen", strings.NewReader(`{"mode":"official"}`))
	app.handleProviderBaiduOpenAuthStart(recorder, request, model.Provider{ID: "provider-a", Type: "baiduopen"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var response baiduOpenAuthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SessionID == "" || response.Mode != baiduOpenAuthModeOfficial || response.State != "pending" {
		t.Fatalf("response = %+v", response)
	}
	wantRedirectURI := "https://nya.example/root/api/v1/providers/provider-a/auth/baiduopen/callback"
	if response.RedirectURI != wantRedirectURI {
		t.Fatalf("redirect_uri = %q, want %q", response.RedirectURI, wantRedirectURI)
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

func TestBaiduOpenCallbackURIRejectsInvalidPublicBaseURL(t *testing.T) {
	app := &App{config: config.Config{Server: config.ServerConfig{PublicBaseURL: "https://nya.example/root?redirect=evil"}}}
	if _, err := app.baiduOpenCallbackURI("provider-a"); err == nil {
		t.Fatal("baiduOpenCallbackURI succeeded with a query string")
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

func TestHandleProviderBaiduOpenTokenImportValidatesRefreshTokenBeforeSaving(t *testing.T) {
	app, db := newOpen115TokenImportTestApp(t)
	if _, err := db.Exec(`
INSERT INTO providers (id, type, name, root_path)
VALUES ('provider-a', 'baiduopen', 'Baidu', '/')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO provider_secrets (provider_id, secret_type, secret_value, masked_value)
VALUES
    ('provider-a', 'client_id', 'client-a', 'cl****-a'),
    ('provider-a', 'client_secret', 'secret-a', 'se****-a'),
    ('provider-a', 'refresh_token', 'saved-refresh', 'sa*********sh')`); err != nil {
		t.Fatal(err)
	}
	app.baiduOAuthHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/rest/2.0/xpan/nas":
			if request.URL.Query().Get("method") != "uinfo" || request.URL.Query().Get("access_token") != "broker-access" {
				t.Fatalf("access token validation query = %s", request.URL.RawQuery)
			}
			return oauthBrokerJSONResponse(http.StatusOK, `{"errno":0,"baidu_name":"test"}`), nil
		case "/oauth/2.0/token":
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			values, err := url.ParseQuery(string(body))
			if err != nil {
				return nil, err
			}
			if values.Get("grant_type") != "refresh_token" ||
				values.Get("client_id") != "client-a" ||
				values.Get("client_secret") != "secret-a" ||
				values.Get("refresh_token") != "saved-refresh" {
				t.Fatalf("refresh form = %s", body)
			}
			return oauthBrokerJSONResponse(http.StatusOK, `{"access_token":"validated-access","refresh_token":"validated-refresh","expires_in":2592000}`), nil
		default:
			t.Fatalf("unexpected validation path %s", request.URL.Path)
			return nil, nil
		}
	})}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/providers/provider-a/auth/baiduopen/tokens", strings.NewReader(`{
		"access_token": "broker-access",
		"refresh_token": ""
	}`))
	app.handleProviderBaiduOpenTokenImport(recorder, request, "provider-a")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	secrets, err := app.loadProviderSecretValues(request.Context(), "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if secrets["access_token"] != "validated-access" || secrets["refresh_token"] != "validated-refresh" {
		t.Fatalf("secrets = %+v", secrets)
	}
}

func TestHandleProviderBaiduOpenTokenImportDoesNotSaveInvalidToken(t *testing.T) {
	app, db := newOpen115TokenImportTestApp(t)
	if _, err := db.Exec(`
INSERT INTO providers (id, type, name, root_path)
VALUES ('provider-a', 'baiduopen', 'Baidu', '/')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO provider_secrets (provider_id, secret_type, secret_value, masked_value)
VALUES
    ('provider-a', 'client_id', 'client-a', 'cl****-a'),
    ('provider-a', 'client_secret', 'secret-a', 'se****-a')`); err != nil {
		t.Fatal(err)
	}
	app.baiduOAuthHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/rest/2.0/xpan/nas" {
			return oauthBrokerJSONResponse(http.StatusOK, `{"errno":0,"baidu_name":"test"}`), nil
		}
		return oauthBrokerJSONResponse(http.StatusBadRequest, `{"error":"invalid_grant","error_description":"Invalid refresh token"}`), nil
	})}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/providers/provider-a/auth/baiduopen/tokens", strings.NewReader(`{
		"access_token": "invalid-access",
		"refresh_token": "invalid-refresh"
	}`))
	app.handleProviderBaiduOpenTokenImport(recorder, request, "provider-a")
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	secrets, err := app.loadProviderSecretValues(request.Context(), "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if secrets["access_token"] != "" || secrets["refresh_token"] != "" {
		t.Fatalf("invalid tokens were saved: %+v", secrets)
	}
}

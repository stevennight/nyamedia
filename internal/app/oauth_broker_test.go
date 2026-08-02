package app

import (
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func oauthBrokerJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestHandleProviderBaiduOpenAuthStartBuildsBrokerRelayURL(t *testing.T) {
	app, db := newOpen115TokenImportTestApp(t)
	app.config = config.Config{Server: config.ServerConfig{PublicBaseURL: "https://nya.example"}}
	app.baiduAuthFlows = make(map[string]*baiduOpenAuthFlow)
	if _, err := db.Exec(`
INSERT INTO provider_secrets (provider_id, secret_type, secret_value, masked_value)
VALUES
    ('provider-a', 'client_id', 'baidu-client', 'ba****nt'),
    ('provider-a', 'client_secret', 'baidu-secret', 'ba****et'),
    ('provider-a', 'oauth_broker_base_url', 'https://broker.example', 'br****le'),
    ('provider-a', 'oauth_broker_client_id', 'broker-client', 'br****nt'),
    ('provider-a', 'oauth_broker_token', 'broker-token', 'br****en')`); err != nil {
		t.Fatal(err)
	}

	var relayRequest struct {
		Provider    string `json:"provider"`
		ReturnURI   string `json:"return_uri"`
		ClientState string `json:"client_state"`
	}
	app.oauthBrokerHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://broker.example/v1/relay/sessions" || request.Method != http.MethodPost {
			t.Fatalf("unexpected broker request: %s %s", request.Method, request.URL)
		}
		if request.Header.Get("X-Broker-Client-ID") != "broker-client" || request.Header.Get("Authorization") != "Bearer broker-token" {
			t.Fatalf("unexpected broker headers: %+v", request.Header)
		}
		if err := json.NewDecoder(request.Body).Decode(&relayRequest); err != nil {
			t.Fatal(err)
		}
		return oauthBrokerJSONResponse(http.StatusCreated, `{
            "session_id":"broker-session",
            "state":"broker-state",
            "redirect_uri":"https://broker.example/v1/callbacks/baidu",
            "expires_at":"2099-01-01T00:00:00Z"
        }`), nil
	})}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/providers/provider-a/auth/baiduopen", strings.NewReader(`{"mode":"broker_relay"}`))
	app.handleProviderBaiduOpenAuthStart(recorder, request, model.Provider{ID: "provider-a", Type: "baiduopen"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var response baiduOpenAuthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Mode != baiduOpenAuthModeBrokerRelay || response.RedirectURI != "https://broker.example/v1/callbacks/baidu" {
		t.Fatalf("response = %+v", response)
	}
	if relayRequest.Provider != "baidu" || relayRequest.ClientState != response.SessionID || relayRequest.ReturnURI != "https://nya.example/api/v1/providers/provider-a/auth/baiduopen/callback" {
		t.Fatalf("relay request = %+v", relayRequest)
	}
	authorizationURL, err := url.Parse(response.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if authorizationURL.Query().Get("state") != "broker-state" || authorizationURL.Query().Get("redirect_uri") != response.RedirectURI {
		t.Fatalf("authorization URL = %s", response.AuthorizationURL)
	}
}

func TestHandleProviderBaiduOpenTokenExchangePollsBrokerStatus(t *testing.T) {
	app, db := newOpen115TokenImportTestApp(t)
	app.baiduAuthFlows = make(map[string]*baiduOpenAuthFlow)
	if _, err := db.Exec(`
INSERT INTO provider_secrets (provider_id, secret_type, secret_value, masked_value)
VALUES
    ('provider-a', 'oauth_broker_base_url', 'https://broker.example', 'br****le'),
    ('provider-a', 'oauth_broker_client_id', 'broker-client', 'br****nt'),
    ('provider-a', 'oauth_broker_token', 'broker-token', 'br****en')`); err != nil {
		t.Fatal(err)
	}

	app.oauthBrokerHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.Method + " " + request.URL.Path {
		case "POST /v1/token-exchange/sessions":
			return oauthBrokerJSONResponse(http.StatusCreated, `{
                "session_id":"broker-session",
                "start_url":"https://broker.example/authorize/launch-token",
                "expires_at":"2099-01-01T00:00:00Z"
            }`), nil
		case "GET /v1/token-exchange/sessions/broker-session":
			return oauthBrokerJSONResponse(http.StatusOK, `{
                "session_id":"broker-session",
                "provider":"baidu",
                "status":"completed",
                "failure_code":"",
                "expires_at":"2099-01-01T00:00:00Z",
                "completed_at":"2098-12-31T23:59:00Z"
            }`), nil
		default:
			t.Fatalf("unexpected broker request: %s %s", request.Method, request.URL)
			return nil, nil
		}
	})}

	startRecorder := httptest.NewRecorder()
	startRequest := httptest.NewRequest(http.MethodPost, "/api/v1/providers/provider-a/auth/baiduopen", strings.NewReader(`{"mode":"broker_token_exchange"}`))
	providerModel := model.Provider{ID: "provider-a", Type: "baiduopen"}
	app.handleProviderBaiduOpenAuthStart(startRecorder, startRequest, providerModel)
	if startRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", startRecorder.Code, startRecorder.Body.String())
	}
	var started baiduOpenAuthResponse
	if err := json.Unmarshal(startRecorder.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started.AuthorizationURL != "https://broker.example/authorize/launch-token" || started.Mode != baiduOpenAuthModeTokenExchange {
		t.Fatalf("start response = %+v", started)
	}

	statusRecorder := httptest.NewRecorder()
	statusRequest := httptest.NewRequest(http.MethodGet, "/api/v1/providers/provider-a/auth/baiduopen?session_id="+url.QueryEscape(started.SessionID), nil)
	app.handleProviderBaiduOpenAuthStatus(statusRecorder, statusRequest, providerModel)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var status baiduOpenAuthResponse
	if err := json.Unmarshal(statusRecorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.State != "completed" || status.AuthorizationURL != "" || !strings.Contains(status.Message, "复制") {
		t.Fatalf("status response = %+v", status)
	}
}

func TestOAuthBrokerErrorKeepsRequestIDWithoutResponseBody(t *testing.T) {
	app := &App{oauthBrokerHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		response := oauthBrokerJSONResponse(http.StatusUnauthorized, `{"error":{"code":"unauthorized","message":"invalid broker credential","request_id":"request-1"}}`)
		response.Header.Set("X-Request-ID", "request-header")
		return response, nil
	})}}
	_, err := app.createOAuthBrokerTokenExchangeSession(t.Context(), oauthBrokerCredentials{
		BaseURL: "https://broker.example", ClientID: "client", Token: "token",
	})
	if err == nil || !strings.Contains(err.Error(), "request-1") || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateOAuthBrokerBaseURLRequiresHTTPS(t *testing.T) {
	for _, value := range []string{"", "http://broker.example", "https://user@broker.example", "https://broker.example?token=secret"} {
		if _, err := validateOAuthBrokerBaseURL(value); err == nil {
			t.Fatalf("validateOAuthBrokerBaseURL(%q) succeeded", value)
		}
	}
	if got, err := validateOAuthBrokerBaseURL("https://broker.example/"); err != nil || got != "https://broker.example" {
		t.Fatalf("got %q, err = %v", got, err)
	}
}

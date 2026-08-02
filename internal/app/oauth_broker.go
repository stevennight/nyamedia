package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const oauthBrokerResponseLimit = 1 << 20

type oauthBrokerCredentials struct {
	BaseURL  string
	ClientID string
	Token    string
}

type oauthBrokerRelaySession struct {
	SessionID   string `json:"session_id"`
	State       string `json:"state"`
	RedirectURI string `json:"redirect_uri"`
	ExpiresAt   string `json:"expires_at"`
}

type oauthBrokerTokenExchangeSession struct {
	SessionID string `json:"session_id"`
	StartURL  string `json:"start_url"`
	ExpiresAt string `json:"expires_at"`
}

type oauthBrokerTokenExchangeStatus struct {
	SessionID   string `json:"session_id"`
	Provider    string `json:"provider"`
	Status      string `json:"status"`
	FailureCode string `json:"failure_code"`
	ExpiresAt   string `json:"expires_at"`
	CompletedAt string `json:"completed_at"`
}

type oauthBrokerErrorResponse struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

type oauthBrokerError struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
}

func (e *oauthBrokerError) Error() string {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = http.StatusText(e.StatusCode)
	}
	if e.Code != "" {
		message = e.Code + ": " + message
	}
	if e.RequestID != "" {
		message += " (request_id: " + e.RequestID + ")"
	}
	return message
}

func newOAuthBrokerHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func validateOAuthBrokerBaseURL(value string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("broker base_url must be an absolute HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("broker base_url must not contain user info, a query string, or a fragment")
	}
	return parsed.String(), nil
}

func (a *App) createOAuthBrokerRelaySession(
	ctx context.Context,
	credentials oauthBrokerCredentials,
	returnURI,
	clientState string,
) (*oauthBrokerRelaySession, error) {
	payload := struct {
		Provider    string `json:"provider"`
		ReturnURI   string `json:"return_uri"`
		ClientState string `json:"client_state"`
	}{
		Provider:    "baidu",
		ReturnURI:   returnURI,
		ClientState: clientState,
	}
	var result oauthBrokerRelaySession
	if err := a.callOAuthBroker(ctx, credentials, http.MethodPost, "/v1/relay/sessions", payload, http.StatusCreated, &result); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.SessionID) == "" || strings.TrimSpace(result.State) == "" || strings.TrimSpace(result.RedirectURI) == "" || strings.TrimSpace(result.ExpiresAt) == "" {
		return nil, fmt.Errorf("oauth broker relay response is incomplete")
	}
	if _, err := validateBaiduOpenRedirectURI(result.RedirectURI); err != nil {
		return nil, fmt.Errorf("oauth broker returned an invalid provider redirect_uri")
	}
	if _, err := time.Parse(time.RFC3339, result.ExpiresAt); err != nil {
		return nil, fmt.Errorf("oauth broker returned an invalid expires_at")
	}
	return &result, nil
}

func (a *App) createOAuthBrokerTokenExchangeSession(
	ctx context.Context,
	credentials oauthBrokerCredentials,
) (*oauthBrokerTokenExchangeSession, error) {
	payload := struct {
		Provider string `json:"provider"`
	}{Provider: "baidu"}
	var result oauthBrokerTokenExchangeSession
	if err := a.callOAuthBroker(ctx, credentials, http.MethodPost, "/v1/token-exchange/sessions", payload, http.StatusCreated, &result); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.SessionID) == "" || strings.TrimSpace(result.StartURL) == "" || strings.TrimSpace(result.ExpiresAt) == "" {
		return nil, fmt.Errorf("oauth broker token exchange response is incomplete")
	}
	startURL, err := url.Parse(result.StartURL)
	if err != nil || startURL.Scheme != "https" || startURL.Host == "" {
		return nil, fmt.Errorf("oauth broker returned an invalid start_url")
	}
	if _, err := time.Parse(time.RFC3339, result.ExpiresAt); err != nil {
		return nil, fmt.Errorf("oauth broker returned an invalid expires_at")
	}
	return &result, nil
}

func (a *App) getOAuthBrokerTokenExchangeStatus(
	ctx context.Context,
	credentials oauthBrokerCredentials,
	sessionID string,
) (*oauthBrokerTokenExchangeStatus, error) {
	var result oauthBrokerTokenExchangeStatus
	endpoint := "/v1/token-exchange/sessions/" + url.PathEscape(strings.TrimSpace(sessionID))
	if err := a.callOAuthBroker(ctx, credentials, http.MethodGet, endpoint, nil, http.StatusOK, &result); err != nil {
		return nil, err
	}
	if result.SessionID != sessionID || result.Provider != "baidu" {
		return nil, fmt.Errorf("oauth broker token exchange status does not match the requested session")
	}
	switch result.Status {
	case "pending", "completed", "failed", "expired":
	default:
		return nil, fmt.Errorf("oauth broker returned an unknown token exchange status")
	}
	return &result, nil
}

func (a *App) callOAuthBroker(
	ctx context.Context,
	credentials oauthBrokerCredentials,
	method,
	endpoint string,
	payload any,
	wantStatus int,
	result any,
) error {
	baseURL, err := validateOAuthBrokerBaseURL(credentials.BaseURL)
	if err != nil {
		return err
	}
	if strings.TrimSpace(credentials.ClientID) == "" || strings.TrimSpace(credentials.Token) == "" {
		return fmt.Errorf("oauth broker client_id and token are required")
	}

	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode oauth broker request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+endpoint, body)
	if err != nil {
		return fmt.Errorf("create oauth broker request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Broker-Client-ID", strings.TrimSpace(credentials.ClientID))
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(credentials.Token))
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := a.oauthBrokerHTTPClient
	if client == nil {
		client = newOAuthBrokerHTTPClient()
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("oauth broker request failed: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, oauthBrokerResponseLimit+1))
	if err != nil {
		return fmt.Errorf("read oauth broker response: %w", err)
	}
	if len(responseBody) > oauthBrokerResponseLimit {
		return fmt.Errorf("oauth broker response exceeds size limit")
	}
	if response.StatusCode != wantStatus {
		brokerErr := &oauthBrokerError{StatusCode: response.StatusCode, RequestID: strings.TrimSpace(response.Header.Get("X-Request-ID"))}
		var decoded oauthBrokerErrorResponse
		if json.Unmarshal(responseBody, &decoded) == nil {
			brokerErr.Code = strings.TrimSpace(decoded.Error.Code)
			brokerErr.Message = strings.TrimSpace(decoded.Error.Message)
			if strings.TrimSpace(decoded.Error.RequestID) != "" {
				brokerErr.RequestID = strings.TrimSpace(decoded.Error.RequestID)
			}
		}
		return brokerErr
	}
	if err := json.Unmarshal(responseBody, result); err != nil {
		return fmt.Errorf("decode oauth broker response: %w", err)
	}
	return nil
}

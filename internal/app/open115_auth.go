package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"NyaMedia/internal/model"
)

const (
	open115AuthURL       = "https://passportapi.115.com/open/authDeviceCode"
	open115TokenURL      = "https://passportapi.115.com/open/deviceCodeToToken"
	open115QRStatusURL   = "https://qrcodeapi.115.com/get/status/"
	open115AuthUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

type open115AuthFlow struct {
	ID           string
	ProviderID   string
	ClientID     string
	UID          string
	PollTime     string
	Sign         string
	QRCode       string
	CodeVerifier string
	State        string
	Message      string
	AccessToken  string
	RefreshToken string
	CreatedAt    string
	UpdatedAt    string
	CompletedAt  string
}

type open115AuthStartPayload struct {
	ClientID string `json:"client_id"`
}

type open115AuthResponse struct {
	SessionID    string `json:"session_id"`
	ProviderID   string `json:"provider_id"`
	ClientID     string `json:"client_id"`
	QRCode       string `json:"qr_code,omitempty"`
	State        string `json:"state"`
	Message      string `json:"message,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	CompletedAt  string `json:"completed_at,omitempty"`
}

type open115DeviceCodeEnvelope struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
	Error   string `json:"error"`
	Errno   int64  `json:"errno"`
	Data    struct {
		UID    string `json:"uid"`
		Time   int64  `json:"time"`
		QRCode string `json:"qrcode"`
		Sign   string `json:"sign"`
	} `json:"data"`
}

type open115QRStatusEnvelope struct {
	State int64  `json:"state"`
	Code  int64  `json:"code"`
	Msg   string `json:"msg"`
	Data  struct {
		Msg     string `json:"msg"`
		Status  int64  `json:"status"`
		Version string `json:"version"`
	} `json:"data"`
}

type open115TokenEnvelope struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
	Error   string `json:"error"`
	Errno   int64  `json:"errno"`
	Data    struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	} `json:"data"`
}

func (a *App) handleProvider115OpenAuth(w http.ResponseWriter, r *http.Request, providerID string) {
	providerModel, err := a.providers.Get(r.Context(), providerID)
	if err != nil {
		handleStorageError(w, err)
		return
	}
	if providerModel == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	if providerModel.Type != "115open" {
		writeError(w, http.StatusBadRequest, "provider type does not support 115open auth")
		return
	}

	switch r.Method {
	case http.MethodPost:
		a.handleProvider115OpenAuthStart(w, r, *providerModel)
	case http.MethodGet:
		a.handleProvider115OpenAuthStatus(w, r, *providerModel)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleProvider115OpenAuthStart(w http.ResponseWriter, r *http.Request, providerModel model.Provider) {
	var payload open115AuthStartPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	clientID := strings.TrimSpace(payload.ClientID)
	if clientID == "" {
		secrets, err := a.loadProviderSecretValues(r.Context(), providerModel.ID)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		clientID = strings.TrimSpace(secrets["client_id"])
	}
	if clientID == "" {
		writeError(w, http.StatusBadRequest, "client_id is required for 115open auth")
		return
	}

	codeVerifier, err := newOpen115CodeVerifier()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	deviceCode, err := requestOpen115DeviceCode(r.Context(), clientID, codeVerifier)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	flow := &open115AuthFlow{
		ID:           newID("open115auth"),
		ProviderID:   providerModel.ID,
		ClientID:     clientID,
		UID:          deviceCode.Data.UID,
		PollTime:     fmt.Sprintf("%d", deviceCode.Data.Time),
		Sign:         deviceCode.Data.Sign,
		QRCode:       deviceCode.Data.QRCode,
		CodeVerifier: codeVerifier,
		State:        "pending",
		Message:      "等待扫码",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	a.authMu.Lock()
	authPruneExpiredLocked(a.authFlows)
	a.authFlows[flow.ID] = flow
	a.authMu.Unlock()

	writeJSON(w, http.StatusOK, toOpen115AuthResponse(flow))
}

func (a *App) handleProvider115OpenAuthStatus(w http.ResponseWriter, r *http.Request, providerModel model.Provider) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id is required")
		return
	}

	flow, ok := a.getOpen115AuthFlow(sessionID, providerModel.ID)
	if !ok {
		writeError(w, http.StatusNotFound, "auth session not found")
		return
	}

	if !isOpen115AuthTerminal(flow.State) {
		if err := a.pollOpen115AuthFlow(r.Context(), flow); err != nil {
			a.updateOpen115AuthFlow(flow.ID, func(current *open115AuthFlow) {
				current.State = "error"
				current.Message = err.Error()
				current.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			})
			flow, _ = a.getOpen115AuthFlow(sessionID, providerModel.ID)
		}
	}

	writeJSON(w, http.StatusOK, toOpen115AuthResponse(flow))
}

func (a *App) pollOpen115AuthFlow(ctx context.Context, flow *open115AuthFlow) error {
	status, err := requestOpen115QRStatus(ctx, flow)
	if err != nil {
		return err
	}

	updatedAt := time.Now().UTC().Format(time.RFC3339)
	message := strings.TrimSpace(status.Data.Msg)
	if message == "" {
		message = strings.TrimSpace(status.Msg)
	}

	a.updateOpen115AuthFlow(flow.ID, func(current *open115AuthFlow) {
		current.UpdatedAt = updatedAt
		switch {
		case status.State == 0 || status.Data.Status == -1:
			current.State = "expired"
			current.Message = fallbackString(message, "二维码已过期")
			current.CompletedAt = updatedAt
		case status.Data.Status == -2:
			current.State = "cancelled"
			current.Message = fallbackString(message, "已取消授权")
			current.CompletedAt = updatedAt
		case status.Data.Status == 1:
			current.State = "scanned"
			current.Message = fallbackString(message, "扫码成功，等待确认")
		case status.Data.Status == 2:
			current.State = "confirming"
			current.Message = fallbackString(message, "已确认授权，正在换取令牌")
		default:
			current.State = "pending"
			current.Message = fallbackString(message, "等待扫码")
		}
	})

	if status.Data.Status != 2 {
		return nil
	}

	tokens, err := requestOpen115Tokens(ctx, flow)
	if err != nil {
		return err
	}

	a.persistProviderToken(flow.ProviderID, "client_id", flow.ClientID)
	a.persistProviderToken(flow.ProviderID, "access_token", tokens.Data.AccessToken)
	a.persistProviderToken(flow.ProviderID, "refresh_token", tokens.Data.RefreshToken)

	a.updateOpen115AuthFlow(flow.ID, func(current *open115AuthFlow) {
		current.State = "authorized"
		current.Message = "授权成功"
		current.AccessToken = tokens.Data.AccessToken
		current.RefreshToken = tokens.Data.RefreshToken
		current.UpdatedAt = updatedAt
		current.CompletedAt = updatedAt
	})
	return nil
}

func (a *App) getOpen115AuthFlow(sessionID, providerID string) (*open115AuthFlow, bool) {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	authPruneExpiredLocked(a.authFlows)
	flow, ok := a.authFlows[sessionID]
	if !ok || flow.ProviderID != providerID {
		return nil, false
	}
	copy := *flow
	return &copy, true
}

func (a *App) updateOpen115AuthFlow(sessionID string, update func(flow *open115AuthFlow)) {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	flow, ok := a.authFlows[sessionID]
	if !ok {
		return
	}
	update(flow)
}

func toOpen115AuthResponse(flow *open115AuthFlow) open115AuthResponse {
	if flow == nil {
		return open115AuthResponse{}
	}
	return open115AuthResponse{
		SessionID:    flow.ID,
		ProviderID:   flow.ProviderID,
		ClientID:     flow.ClientID,
		QRCode:       flow.QRCode,
		State:        flow.State,
		Message:      flow.Message,
		AccessToken:  flow.AccessToken,
		RefreshToken: flow.RefreshToken,
		CreatedAt:    flow.CreatedAt,
		UpdatedAt:    flow.UpdatedAt,
		CompletedAt:  flow.CompletedAt,
	}
}

func requestOpen115DeviceCode(ctx context.Context, clientID, codeVerifier string) (*open115DeviceCodeEnvelope, error) {
	values := url.Values{}
	values.Set("client_id", clientID)
	values.Set("code_challenge", open115CodeChallenge(codeVerifier))
	values.Set("code_challenge_method", "sha256")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, open115AuthURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", open115AuthUserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var envelope open115DeviceCodeEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		if envelope.Error != "" {
			return nil, fmt.Errorf("115open auth error code=%d errno=%d error=%s", envelope.Code, envelope.Errno, envelope.Error)
		}
		return nil, fmt.Errorf("115open auth error code=%d message=%s", envelope.Code, envelope.Message)
	}
	if envelope.Data.UID == "" || envelope.Data.QRCode == "" {
		return nil, fmt.Errorf("115open auth returned empty qr data")
	}
	return &envelope, nil
}

func requestOpen115QRStatus(ctx context.Context, flow *open115AuthFlow) (*open115QRStatusEnvelope, error) {
	values := url.Values{}
	values.Set("uid", flow.UID)
	values.Set("time", flow.PollTime)
	values.Set("sign", flow.Sign)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, open115QRStatusURL+"?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", open115AuthUserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var envelope open115QRStatusEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	return &envelope, nil
}

func requestOpen115Tokens(ctx context.Context, flow *open115AuthFlow) (*open115TokenEnvelope, error) {
	values := url.Values{}
	values.Set("uid", flow.UID)
	values.Set("code_verifier", flow.CodeVerifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, open115TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", open115AuthUserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var envelope open115TokenEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		if envelope.Error != "" {
			return nil, fmt.Errorf("115open token error code=%d errno=%d error=%s", envelope.Code, envelope.Errno, envelope.Error)
		}
		return nil, fmt.Errorf("115open token error code=%d message=%s", envelope.Code, envelope.Message)
	}
	if envelope.Data.AccessToken == "" || envelope.Data.RefreshToken == "" {
		return nil, fmt.Errorf("115open token response missing access_token or refresh_token")
	}
	return &envelope, nil
}

func newOpen115CodeVerifier() (string, error) {
	buf := make([]byte, 48)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func open115CodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func authPruneExpiredLocked(flows map[string]*open115AuthFlow) {
	cutoff := time.Now().UTC().Add(-30 * time.Minute)
	for id, flow := range flows {
		updatedAt, err := time.Parse(time.RFC3339, flow.UpdatedAt)
		if err != nil || updatedAt.Before(cutoff) {
			delete(flows, id)
		}
	}
}

func isOpen115AuthTerminal(state string) bool {
	switch state {
	case "authorized", "expired", "cancelled", "error":
		return true
	default:
		return false
	}
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

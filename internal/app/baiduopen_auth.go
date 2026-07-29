package app

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"NyaMedia/internal/model"
	baiduopenprovider "NyaMedia/internal/provider/baiduopen"
)

type baiduOpenAuthFlow struct {
	ID               string
	ProviderID       string
	ClientID         string
	RedirectURI      string
	AuthorizationURL string
	State            string
	Message          string
	CreatedAt        string
	UpdatedAt        string
	CompletedAt      string
}

type baiduOpenCredentialsPayload struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type baiduOpenAuthStartPayload struct {
	RedirectURI string `json:"redirect_uri"`
}

type baiduOpenAuthResponse struct {
	SessionID        string `json:"session_id"`
	ProviderID       string `json:"provider_id"`
	AuthorizationURL string `json:"authorization_url,omitempty"`
	RedirectURI      string `json:"redirect_uri,omitempty"`
	State            string `json:"state"`
	Message          string `json:"message,omitempty"`
	CreatedAt        string `json:"created_at,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
	CompletedAt      string `json:"completed_at,omitempty"`
}

func (a *App) handleProviderBaiduOpenAuth(w http.ResponseWriter, r *http.Request, providerID string) {
	providerModel, err := a.providers.Get(r.Context(), providerID)
	if err != nil {
		handleStorageError(w, err)
		return
	}
	if providerModel == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	if providerModel.Type != "baiduopen" {
		writeError(w, http.StatusBadRequest, "provider type does not support baiduopen auth")
		return
	}

	switch r.Method {
	case http.MethodPut:
		a.handleProviderBaiduOpenCredentials(w, r, *providerModel)
	case http.MethodPost:
		a.handleProviderBaiduOpenAuthStart(w, r, *providerModel)
	case http.MethodGet:
		a.handleProviderBaiduOpenAuthStatus(w, r, *providerModel)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleProviderBaiduOpenCredentials(w http.ResponseWriter, r *http.Request, providerModel model.Provider) {
	var payload baiduOpenCredentialsPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payload.ClientID = strings.TrimSpace(payload.ClientID)
	payload.ClientSecret = strings.TrimSpace(payload.ClientSecret)
	if payload.ClientID == "" || payload.ClientSecret == "" {
		writeError(w, http.StatusBadRequest, "client_id and client_secret are required")
		return
	}

	credentials := []model.ProviderSecret{
		{
			ProviderID:  providerModel.ID,
			SecretType:  "client_id",
			SecretValue: payload.ClientID,
			MaskedValue: maskProviderSecret("client_id", payload.ClientID),
		},
		{
			ProviderID:  providerModel.ID,
			SecretType:  "client_secret",
			SecretValue: payload.ClientSecret,
			MaskedValue: maskProviderSecret("client_secret", payload.ClientSecret),
		},
	}
	a.baiduAuthMu.Lock()
	err := a.secrets.ReplaceManyAndInvalidateProviderState(
		r.Context(),
		providerModel.ID,
		credentials,
		[]string{"access_token", "refresh_token", "access_token_expires_at"},
	)
	a.baiduAuthMu.Unlock()
	if err != nil {
		handleStorageError(w, err)
		return
	}
	a.invalidateBaiduOpenProvider(providerModel.ID)

	writeJSON(w, http.StatusOK, map[string]any{
		"provider_id": providerModel.ID,
		"saved":       []string{"client_id", "client_secret"},
	})
}

func (a *App) handleProviderBaiduOpenAuthStart(w http.ResponseWriter, r *http.Request, providerModel model.Provider) {
	var payload baiduOpenAuthStartPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	redirectURI, err := validateBaiduOpenRedirectURI(payload.RedirectURI)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	secrets, err := a.loadProviderSecretValues(r.Context(), providerModel.ID)
	if err != nil {
		a.recordProviderAuthError(r.Context(), providerModel, "baiduopen", "load_secret", err)
		handleStorageError(w, err)
		return
	}
	clientID := strings.TrimSpace(secrets["client_id"])
	if clientID == "" || strings.TrimSpace(secrets["client_secret"]) == "" {
		writeError(w, http.StatusBadRequest, "client_id and client_secret are required before baiduopen auth")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sessionID := newID("baiduauth")
	flow := &baiduOpenAuthFlow{
		ID:          sessionID,
		ProviderID:  providerModel.ID,
		ClientID:    clientID,
		RedirectURI: redirectURI,
		State:       "pending",
		Message:     "等待百度账号授权",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	flow.AuthorizationURL = baiduopenprovider.AuthorizationURL(clientID, redirectURI, sessionID)

	a.baiduAuthMu.Lock()
	pruneBaiduOpenAuthFlowsLocked(a.baiduAuthFlows)
	a.baiduAuthFlows[sessionID] = flow
	a.baiduAuthMu.Unlock()

	writeJSON(w, http.StatusOK, toBaiduOpenAuthResponse(flow))
}

func (a *App) handleProviderBaiduOpenAuthStatus(w http.ResponseWriter, r *http.Request, providerModel model.Provider) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id is required")
		return
	}
	flow, ok := a.getBaiduOpenAuthFlow(sessionID, providerModel.ID)
	if !ok {
		writeError(w, http.StatusNotFound, "auth session not found")
		return
	}
	writeJSON(w, http.StatusOK, toBaiduOpenAuthResponse(flow))
}

func (a *App) handleProviderBaiduOpenCallback(w http.ResponseWriter, r *http.Request, providerID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	flow, ok := a.getBaiduOpenAuthFlow(state, providerID)
	if !ok {
		writeBaiduOpenCallbackPage(w, http.StatusBadRequest, "授权会话无效或已过期")
		return
	}
	if oauthError := strings.TrimSpace(r.URL.Query().Get("error")); oauthError != "" {
		description := strings.TrimSpace(r.URL.Query().Get("error_description"))
		message := fallbackString(description, oauthError)
		a.finishBaiduOpenAuthFlow(flow.ID, "error", message)
		writeBaiduOpenCallbackPage(w, http.StatusBadRequest, "百度授权失败："+message)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		a.finishBaiduOpenAuthFlow(flow.ID, "error", "authorization code is missing")
		writeBaiduOpenCallbackPage(w, http.StatusBadRequest, "授权回调缺少 code")
		return
	}

	providerModel, err := a.providers.Get(r.Context(), providerID)
	if err != nil || providerModel == nil || providerModel.Type != "baiduopen" {
		a.finishBaiduOpenAuthFlow(flow.ID, "error", "provider is unavailable")
		writeBaiduOpenCallbackPage(w, http.StatusBadRequest, "百度数据源不可用")
		return
	}
	secrets, err := a.loadProviderSecretValues(r.Context(), providerID)
	if err != nil {
		a.recordProviderAuthError(r.Context(), *providerModel, "baiduopen", "load_secret", err)
		a.finishBaiduOpenAuthFlow(flow.ID, "error", err.Error())
		writeBaiduOpenCallbackPage(w, http.StatusInternalServerError, "读取百度应用凭据失败")
		return
	}
	clientID := strings.TrimSpace(secrets["client_id"])
	clientSecret := strings.TrimSpace(secrets["client_secret"])
	if clientID == "" || clientSecret == "" || clientID != flow.ClientID {
		a.finishBaiduOpenAuthFlow(flow.ID, "error", "application credentials changed during authorization")
		writeBaiduOpenCallbackPage(w, http.StatusBadRequest, "授权期间应用凭据已变更，请重新发起授权")
		return
	}

	token, err := baiduopenprovider.ExchangeAuthorizationCode(
		r.Context(),
		clientID,
		clientSecret,
		code,
		flow.RedirectURI,
	)
	if err != nil {
		a.recordProviderAuthError(r.Context(), *providerModel, "baiduopen", "exchange_code", err)
		a.finishBaiduOpenAuthFlow(flow.ID, "error", err.Error())
		writeBaiduOpenCallbackPage(w, http.StatusBadGateway, "换取百度网盘 Token 失败："+err.Error())
		return
	}
	if !a.persistProviderBaiduOpenTokens(
		providerID,
		clientID,
		clientSecret,
		token.AccessToken,
		token.RefreshToken,
		baiduOpenAccessTokenExpiresAt(token.ExpiresIn, time.Now()),
	) {
		a.finishBaiduOpenAuthFlow(flow.ID, "error", "application credentials changed before token persistence")
		writeBaiduOpenCallbackPage(w, http.StatusConflict, "应用凭据已变更，请重新发起授权")
		return
	}
	a.invalidateBaiduOpenProvider(providerID)
	a.finishBaiduOpenAuthFlow(flow.ID, "authorized", "授权成功")
	writeBaiduOpenCallbackPage(w, http.StatusOK, "百度网盘授权成功，可以关闭此窗口")
}

func (a *App) persistProviderBaiduOpenTokens(
	providerID,
	expectedClientID,
	expectedClientSecret,
	accessToken,
	refreshToken,
	expiresAt string,
) bool {
	accessToken = strings.TrimSpace(accessToken)
	refreshToken = strings.TrimSpace(refreshToken)
	expiresAt = strings.TrimSpace(expiresAt)
	if accessToken == "" || refreshToken == "" {
		return false
	}

	a.baiduAuthMu.Lock()
	defer a.baiduAuthMu.Unlock()
	current, err := a.loadProviderSecretValues(context.Background(), providerID)
	if err != nil {
		a.recordSystemEvent(context.Background(), "provider_auth_error", "error", "provider", "failed to load provider credential", map[string]any{
			"provider_id": providerID,
			"error":       err.Error(),
		})
		return false
	}
	if strings.TrimSpace(current["client_id"]) != strings.TrimSpace(expectedClientID) ||
		strings.TrimSpace(current["client_secret"]) != strings.TrimSpace(expectedClientSecret) {
		return false
	}
	items := []model.ProviderSecret{
		{
			ProviderID:  providerID,
			SecretType:  "access_token",
			SecretValue: accessToken,
			MaskedValue: maskProviderSecret("access_token", accessToken),
		},
		{
			ProviderID:  providerID,
			SecretType:  "refresh_token",
			SecretValue: refreshToken,
			MaskedValue: maskProviderSecret("refresh_token", refreshToken),
		},
	}
	if expiresAt != "" {
		items = append(items, model.ProviderSecret{
			ProviderID:  providerID,
			SecretType:  "access_token_expires_at",
			SecretValue: expiresAt,
			MaskedValue: maskProviderSecret("access_token_expires_at", expiresAt),
		})
	}
	if err := a.secrets.ReplaceMany(
		context.Background(),
		providerID,
		items,
		[]string{"access_token", "refresh_token", "access_token_expires_at"},
	); err != nil {
		a.recordSystemEvent(context.Background(), "provider_auth_error", "error", "provider", "failed to persist provider credential", map[string]any{
			"provider_id": providerID,
			"secret_type": "access_token",
			"error":       err.Error(),
		})
		return false
	}
	return true
}

func (a *App) getBaiduOpenAuthFlow(sessionID, providerID string) (*baiduOpenAuthFlow, bool) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, false
	}
	a.baiduAuthMu.Lock()
	defer a.baiduAuthMu.Unlock()
	pruneBaiduOpenAuthFlowsLocked(a.baiduAuthFlows)
	flow, ok := a.baiduAuthFlows[sessionID]
	if !ok || flow.ProviderID != providerID {
		return nil, false
	}
	copy := *flow
	return &copy, true
}

func (a *App) finishBaiduOpenAuthFlow(sessionID, state, message string) {
	a.baiduAuthMu.Lock()
	defer a.baiduAuthMu.Unlock()
	flow, ok := a.baiduAuthFlows[sessionID]
	if !ok {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	flow.State = state
	flow.Message = strings.TrimSpace(message)
	flow.UpdatedAt = now
	flow.CompletedAt = now
}

func toBaiduOpenAuthResponse(flow *baiduOpenAuthFlow) baiduOpenAuthResponse {
	if flow == nil {
		return baiduOpenAuthResponse{}
	}
	return baiduOpenAuthResponse{
		SessionID:        flow.ID,
		ProviderID:       flow.ProviderID,
		AuthorizationURL: flow.AuthorizationURL,
		RedirectURI:      flow.RedirectURI,
		State:            flow.State,
		Message:          flow.Message,
		CreatedAt:        flow.CreatedAt,
		UpdatedAt:        flow.UpdatedAt,
		CompletedAt:      flow.CompletedAt,
	}
}

func validateBaiduOpenRedirectURI(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("redirect_uri must be an absolute http or https URL")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("redirect_uri must not contain user info or a fragment")
	}
	return parsed.String(), nil
}

func pruneBaiduOpenAuthFlowsLocked(flows map[string]*baiduOpenAuthFlow) {
	cutoff := time.Now().UTC().Add(-30 * time.Minute)
	for id, flow := range flows {
		updatedAt, err := time.Parse(time.RFC3339, flow.UpdatedAt)
		if err != nil || updatedAt.Before(cutoff) {
			delete(flows, id)
		}
	}
}

func baiduOpenAccessTokenExpiresAt(expiresIn int64, now time.Time) string {
	if expiresIn <= 0 {
		return ""
	}
	return now.UTC().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339)
}

func writeBaiduOpenCallbackPage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(
		w,
		"<!doctype html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\"><title>百度网盘授权</title></head><body><p>%s</p></body></html>",
		html.EscapeString(message),
	)
}

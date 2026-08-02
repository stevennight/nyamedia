package app

import (
	"context"
	"errors"
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
	Mode             string
	ClientID         string
	RedirectURI      string
	AuthorizationURL string
	BrokerSessionID  string
	ExpiresAt        string
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
	Mode string `json:"mode"`
}

type baiduOpenAuthResponse struct {
	SessionID        string `json:"session_id"`
	ProviderID       string `json:"provider_id"`
	Mode             string `json:"mode"`
	AuthorizationURL string `json:"authorization_url,omitempty"`
	RedirectURI      string `json:"redirect_uri,omitempty"`
	ExpiresAt        string `json:"expires_at,omitempty"`
	State            string `json:"state"`
	Message          string `json:"message,omitempty"`
	CreatedAt        string `json:"created_at,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
	CompletedAt      string `json:"completed_at,omitempty"`
}

type baiduOpenBrokerConfigPayload struct {
	BaseURL  string `json:"base_url"`
	ClientID string `json:"client_id"`
	Token    string `json:"token"`
}

type baiduOpenBrokerConfigResponse struct {
	BaseURL         string `json:"base_url,omitempty"`
	ClientID        string `json:"client_id,omitempty"`
	TokenConfigured bool   `json:"token_configured"`
	Configured      bool   `json:"configured"`
}

type baiduOpenTokenImportPayload struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

const (
	baiduOpenAuthModeOfficial      = "official"
	baiduOpenAuthModeBrokerRelay   = "broker_relay"
	baiduOpenAuthModeTokenExchange = "broker_token_exchange"
)

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

func (a *App) handleProviderBaiduOpenBrokerConfig(w http.ResponseWriter, r *http.Request, providerID string) {
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

	secrets, err := a.loadProviderSecretValues(r.Context(), providerID)
	if err != nil {
		handleStorageError(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, toBaiduOpenBrokerConfigResponse(secrets))
	case http.MethodPut:
		var payload baiduOpenBrokerConfigPayload
		if err := decodeJSON(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		baseURL, err := validateOAuthBrokerBaseURL(payload.BaseURL)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		clientID := strings.TrimSpace(payload.ClientID)
		token := strings.TrimSpace(payload.Token)
		if token == "" {
			token = strings.TrimSpace(secrets["oauth_broker_token"])
		}
		if clientID == "" || token == "" {
			writeError(w, http.StatusBadRequest, "broker client_id and token are required")
			return
		}
		items := []model.ProviderSecret{
			{ProviderID: providerID, SecretType: "oauth_broker_base_url", SecretValue: baseURL, MaskedValue: maskProviderSecret("oauth_broker_base_url", baseURL)},
			{ProviderID: providerID, SecretType: "oauth_broker_client_id", SecretValue: clientID, MaskedValue: maskProviderSecret("oauth_broker_client_id", clientID)},
			{ProviderID: providerID, SecretType: "oauth_broker_token", SecretValue: token, MaskedValue: maskProviderSecret("oauth_broker_token", token)},
		}
		if err := a.secrets.UpsertMany(r.Context(), items); err != nil {
			handleStorageError(w, err)
			return
		}
		secrets["oauth_broker_base_url"] = baseURL
		secrets["oauth_broker_client_id"] = clientID
		secrets["oauth_broker_token"] = token
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, toBaiduOpenBrokerConfigResponse(secrets))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleProviderBaiduOpenTokenImport(w http.ResponseWriter, r *http.Request, providerID string) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
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
	var payload baiduOpenTokenImportPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payload.AccessToken = strings.TrimSpace(payload.AccessToken)
	payload.RefreshToken = strings.TrimSpace(payload.RefreshToken)
	if payload.AccessToken == "" || payload.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "access_token and refresh_token are required")
		return
	}
	if payload.ExpiresIn < 0 {
		writeError(w, http.StatusBadRequest, "expires_in must not be negative")
		return
	}
	secrets, err := a.loadProviderSecretValues(r.Context(), providerID)
	if err != nil {
		handleStorageError(w, err)
		return
	}
	clientID := strings.TrimSpace(secrets["client_id"])
	clientSecret := strings.TrimSpace(secrets["client_secret"])
	if clientID == "" || clientSecret == "" {
		writeError(w, http.StatusBadRequest, "save the matching baidu client_id and client_secret before importing tokens")
		return
	}
	if err := baiduopenprovider.ValidateAccessToken(r.Context(), a.baiduOAuthHTTPClient, payload.AccessToken); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("baidu access token validation failed: %v", err))
		return
	}
	validatedToken, err := baiduopenprovider.RefreshOAuthToken(
		r.Context(),
		a.baiduOAuthHTTPClient,
		clientID,
		clientSecret,
		payload.RefreshToken,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("baidu token validation failed: %v", err))
		return
	}
	if !a.persistProviderBaiduOpenTokens(
		providerID,
		clientID,
		clientSecret,
		validatedToken.AccessToken,
		validatedToken.RefreshToken,
		baiduOpenAccessTokenExpiresAt(validatedToken.ExpiresIn, time.Now()),
	) {
		writeError(w, http.StatusConflict, "application credentials changed before token persistence")
		return
	}
	a.invalidateBaiduOpenProvider(providerID)
	writeJSON(w, http.StatusOK, map[string]any{
		"provider_id": providerID,
		"saved":       []string{"access_token", "refresh_token", "access_token_expires_at"},
	})
}

func (a *App) handleProviderBaiduOpenAuthStart(w http.ResponseWriter, r *http.Request, providerModel model.Provider) {
	var payload baiduOpenAuthStartPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payload.Mode = strings.TrimSpace(payload.Mode)
	if payload.Mode == "" {
		payload.Mode = baiduOpenAuthModeOfficial
	}
	if payload.Mode != baiduOpenAuthModeOfficial && payload.Mode != baiduOpenAuthModeBrokerRelay && payload.Mode != baiduOpenAuthModeTokenExchange {
		writeError(w, http.StatusBadRequest, "mode must be official, broker_relay, or broker_token_exchange")
		return
	}

	secrets, err := a.loadProviderSecretValues(r.Context(), providerModel.ID)
	if err != nil {
		a.recordProviderAuthError(r.Context(), providerModel, "baiduopen", "load_secret", err)
		handleStorageError(w, err)
		return
	}
	clientID := strings.TrimSpace(secrets["client_id"])
	if payload.Mode != baiduOpenAuthModeTokenExchange && (clientID == "" || strings.TrimSpace(secrets["client_secret"]) == "") {
		writeError(w, http.StatusBadRequest, "client_id and client_secret are required before baiduopen auth")
		return
	}

	redirectURI := ""
	if payload.Mode != baiduOpenAuthModeTokenExchange {
		redirectURI, err = a.baiduOpenCallbackURI(providerModel.ID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sessionID := newID("baiduauth")
	flow := &baiduOpenAuthFlow{
		ID:          sessionID,
		ProviderID:  providerModel.ID,
		Mode:        payload.Mode,
		ClientID:    clientID,
		RedirectURI: redirectURI,
		State:       "pending",
		Message:     "等待百度账号授权",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	switch payload.Mode {
	case baiduOpenAuthModeOfficial:
		flow.AuthorizationURL = baiduopenprovider.AuthorizationURL(clientID, redirectURI, sessionID)
	case baiduOpenAuthModeBrokerRelay:
		brokerCredentials, err := baiduOpenBrokerCredentialsFromSecrets(secrets)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		relay, err := a.createOAuthBrokerRelaySession(r.Context(), brokerCredentials, redirectURI, sessionID)
		if err != nil {
			a.recordProviderAuthError(r.Context(), providerModel, "baiduopen", "broker_relay_create", err)
			writeError(w, oauthBrokerHTTPStatus(err), err.Error())
			return
		}
		flow.BrokerSessionID = relay.SessionID
		flow.RedirectURI = relay.RedirectURI
		flow.ExpiresAt = relay.ExpiresAt
		flow.AuthorizationURL = baiduopenprovider.AuthorizationURL(clientID, relay.RedirectURI, relay.State)
	case baiduOpenAuthModeTokenExchange:
		brokerCredentials, err := baiduOpenBrokerCredentialsFromSecrets(secrets)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		session, err := a.createOAuthBrokerTokenExchangeSession(r.Context(), brokerCredentials)
		if err != nil {
			a.recordProviderAuthError(r.Context(), providerModel, "baiduopen", "broker_token_exchange_create", err)
			writeError(w, oauthBrokerHTTPStatus(err), err.Error())
			return
		}
		flow.BrokerSessionID = session.SessionID
		flow.ExpiresAt = session.ExpiresAt
		flow.AuthorizationURL = session.StartURL
		flow.Message = "等待在 OAuth Broker 中完成授权"
	}

	response := toBaiduOpenAuthResponse(flow)
	if payload.Mode == baiduOpenAuthModeTokenExchange {
		// start_url is a one-time bearer credential and must not be returned by status polling.
		flow.AuthorizationURL = ""
	}

	a.baiduAuthMu.Lock()
	pruneBaiduOpenAuthFlowsLocked(a.baiduAuthFlows)
	a.baiduAuthFlows[sessionID] = flow
	a.baiduAuthMu.Unlock()

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
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
	if flow.Mode == baiduOpenAuthModeTokenExchange && flow.State == "pending" {
		secrets, err := a.loadProviderSecretValues(r.Context(), providerModel.ID)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		brokerCredentials, err := baiduOpenBrokerCredentialsFromSecrets(secrets)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		status, err := a.getOAuthBrokerTokenExchangeStatus(r.Context(), brokerCredentials, flow.BrokerSessionID)
		if err != nil {
			a.recordProviderAuthError(r.Context(), providerModel, "baiduopen", "broker_token_exchange_status", err)
			writeError(w, oauthBrokerHTTPStatus(err), err.Error())
			return
		}
		flow = a.updateBaiduOpenTokenExchangeFlow(flow.ID, status)
		if flow == nil {
			writeError(w, http.StatusNotFound, "auth session not found")
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, toBaiduOpenAuthResponse(flow))
}

func (a *App) handleProviderBaiduOpenCallback(w http.ResponseWriter, r *http.Request, providerID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	clientState := strings.TrimSpace(r.URL.Query().Get("client_state"))
	if (state == "") == (clientState == "") {
		writeBaiduOpenCallbackPage(w, http.StatusBadRequest, "授权回调状态无效")
		return
	}
	flowID := state
	if clientState != "" {
		flowID = clientState
	}
	flow, ok := a.consumeBaiduOpenAuthFlow(flowID, providerID)
	if !ok {
		writeBaiduOpenCallbackPage(w, http.StatusBadRequest, "授权会话无效或已过期")
		return
	}
	if flow.Mode == baiduOpenAuthModeTokenExchange ||
		(flow.Mode == baiduOpenAuthModeOfficial && clientState != "") ||
		(flow.Mode == baiduOpenAuthModeBrokerRelay && state != "") {
		a.finishBaiduOpenAuthFlow(flow.ID, "error", "authorization callback mode mismatch")
		writeBaiduOpenCallbackPage(w, http.StatusBadRequest, "授权回调与会话模式不匹配")
		return
	}
	if flow.Mode == baiduOpenAuthModeBrokerRelay && strings.TrimSpace(r.URL.Query().Get("provider")) != "baidu" {
		a.finishBaiduOpenAuthFlow(flow.ID, "error", "unexpected callback provider")
		writeBaiduOpenCallbackPage(w, http.StatusBadRequest, "授权回调 Provider 无效")
		return
	}

	oauthError := strings.TrimSpace(r.URL.Query().Get("error"))
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if (oauthError == "") == (code == "") {
		a.finishBaiduOpenAuthFlow(flow.ID, "error", "callback must contain exactly one of code or error")
		writeBaiduOpenCallbackPage(w, http.StatusBadRequest, "授权回调必须且只能包含 code 或 error")
		return
	}
	if oauthError != "" {
		description := strings.TrimSpace(r.URL.Query().Get("error_description"))
		message := oauthError
		if flow.Mode == baiduOpenAuthModeOfficial {
			message = fallbackString(description, oauthError)
		} else if oauthError != "authorization_denied" && oauthError != "authorization_failed" {
			message = "authorization_failed"
		}
		a.finishBaiduOpenAuthFlow(flow.ID, "error", message)
		writeBaiduOpenCallbackPage(w, http.StatusBadRequest, "百度授权失败："+message)
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
		a.finishBaiduOpenAuthFlow(flow.ID, "error", "provider token exchange failed")
		writeBaiduOpenCallbackPage(w, http.StatusBadGateway, "换取百度网盘 Token 失败，请重新发起授权")
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

func (a *App) consumeBaiduOpenAuthFlow(sessionID, providerID string) (*baiduOpenAuthFlow, bool) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, false
	}
	a.baiduAuthMu.Lock()
	defer a.baiduAuthMu.Unlock()
	pruneBaiduOpenAuthFlowsLocked(a.baiduAuthFlows)
	flow, ok := a.baiduAuthFlows[sessionID]
	if !ok || flow.ProviderID != providerID || flow.State != "pending" {
		return nil, false
	}
	if flow.ExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339, flow.ExpiresAt)
		if err != nil || !time.Now().UTC().Before(expiresAt) {
			flow.State = "expired"
			flow.Message = "授权会话已过期"
			flow.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			flow.CompletedAt = flow.UpdatedAt
			return nil, false
		}
	}
	flow.State = "processing"
	flow.Message = "正在处理授权回调"
	flow.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	copy := *flow
	return &copy, true
}

func (a *App) updateBaiduOpenTokenExchangeFlow(sessionID string, status *oauthBrokerTokenExchangeStatus) *baiduOpenAuthFlow {
	if status == nil {
		return nil
	}
	a.baiduAuthMu.Lock()
	defer a.baiduAuthMu.Unlock()
	flow, ok := a.baiduAuthFlows[sessionID]
	if !ok {
		return nil
	}
	flow.State = status.Status
	flow.ExpiresAt = status.ExpiresAt
	flow.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	flow.CompletedAt = status.CompletedAt
	switch status.Status {
	case "pending":
		flow.Message = "等待在 OAuth Broker 中完成授权"
	case "completed":
		flow.Message = "Broker 已显示 Token，请复制并导入 NyaMedia"
	case "failed":
		flow.Message = fallbackString(status.FailureCode, "OAuth Broker 授权失败")
	case "expired":
		flow.Message = "OAuth Broker 授权会话已过期"
	}
	copy := *flow
	return &copy
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
		Mode:             flow.Mode,
		AuthorizationURL: flow.AuthorizationURL,
		RedirectURI:      flow.RedirectURI,
		ExpiresAt:        flow.ExpiresAt,
		State:            flow.State,
		Message:          flow.Message,
		CreatedAt:        flow.CreatedAt,
		UpdatedAt:        flow.UpdatedAt,
		CompletedAt:      flow.CompletedAt,
	}
}

func (a *App) baiduOpenCallbackURI(providerID string) (string, error) {
	publicBaseURL := strings.TrimSpace(a.config.Server.PublicBaseURL)
	parsed, err := url.Parse(publicBaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("server.public_base_url must be an absolute http or https URL before baiduopen auth")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("server.public_base_url must not contain user info, a query string, or a fragment")
	}
	callbackURI, err := url.JoinPath(
		publicBaseURL,
		"api",
		"v1",
		"providers",
		providerID,
		"auth",
		"baiduopen",
		"callback",
	)
	if err != nil {
		return "", fmt.Errorf("build baiduopen callback URL: %w", err)
	}
	return validateBaiduOpenRedirectURI(callbackURI)
}

func baiduOpenBrokerCredentialsFromSecrets(secrets map[string]string) (oauthBrokerCredentials, error) {
	baseURL, err := validateOAuthBrokerBaseURL(secrets["oauth_broker_base_url"])
	if err != nil {
		return oauthBrokerCredentials{}, err
	}
	credentials := oauthBrokerCredentials{
		BaseURL:  baseURL,
		ClientID: strings.TrimSpace(secrets["oauth_broker_client_id"]),
		Token:    strings.TrimSpace(secrets["oauth_broker_token"]),
	}
	if credentials.ClientID == "" || credentials.Token == "" {
		return oauthBrokerCredentials{}, fmt.Errorf("oauth broker client_id and token are required")
	}
	return credentials, nil
}

func toBaiduOpenBrokerConfigResponse(secrets map[string]string) baiduOpenBrokerConfigResponse {
	baseURL := strings.TrimSpace(secrets["oauth_broker_base_url"])
	clientID := strings.TrimSpace(secrets["oauth_broker_client_id"])
	tokenConfigured := strings.TrimSpace(secrets["oauth_broker_token"]) != ""
	return baiduOpenBrokerConfigResponse{
		BaseURL:         baseURL,
		ClientID:        clientID,
		TokenConfigured: tokenConfigured,
		Configured:      baseURL != "" && clientID != "" && tokenConfigured,
	}
}

func oauthBrokerHTTPStatus(err error) int {
	var brokerErr *oauthBrokerError
	if !errors.As(err, &brokerErr) {
		return http.StatusBadGateway
	}
	if brokerErr.StatusCode >= http.StatusBadRequest && brokerErr.StatusCode < http.StatusInternalServerError {
		return brokerErr.StatusCode
	}
	return http.StatusBadGateway
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

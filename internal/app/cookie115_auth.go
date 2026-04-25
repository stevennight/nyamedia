package app

import (
	"net/http"
	"strings"
	"time"

	pan115 "github.com/SheltonZhu/115driver/pkg/driver"

	"NyaMedia/internal/model"
)

type cookie115AuthFlow struct {
	ID          string
	ProviderID  string
	Terminal    string
	UID         string
	PollTime    int64
	Sign        string
	QRCode      string
	State       string
	Message     string
	Cookie      string
	CreatedAt   string
	UpdatedAt   string
	CompletedAt string
}

type cookie115AuthStartPayload struct {
	Terminal string `json:"terminal"`
}

type cookie115AuthResponse struct {
	SessionID   string   `json:"session_id"`
	ProviderID  string   `json:"provider_id"`
	Terminal    string   `json:"terminal"`
	QRCode      string   `json:"qr_code,omitempty"`
	State       string   `json:"state"`
	Message     string   `json:"message,omitempty"`
	Cookie      string   `json:"cookie,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
	CompletedAt string   `json:"completed_at,omitempty"`
	Terminals   []string `json:"terminals,omitempty"`
	Recommended []string `json:"recommended_terminals,omitempty"`
}

var cookie115SupportedTerminals = []string{"web", "android", "ios", "tv", "alipaymini", "wechatmini", "qandroid"}
var cookie115RecommendedTerminals = []string{"tv", "alipaymini", "wechatmini", "qandroid"}

func (a *App) handleProvider115CookieAuth(w http.ResponseWriter, r *http.Request, providerID string) {
	providerModel, err := a.providers.Get(r.Context(), providerID)
	if err != nil {
		handleStorageError(w, err)
		return
	}
	if providerModel == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	if providerModel.Type != "115cookie" {
		writeError(w, http.StatusBadRequest, "provider type does not support 115cookie auth")
		return
	}

	switch r.Method {
	case http.MethodPost:
		a.handleProvider115CookieAuthStart(w, r, *providerModel)
	case http.MethodGet:
		a.handleProvider115CookieAuthStatus(w, r, *providerModel)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleProvider115CookieAuthStart(w http.ResponseWriter, r *http.Request, providerModel model.Provider) {
	var payload cookie115AuthStartPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	terminal := strings.TrimSpace(payload.Terminal)
	if terminal == "" {
		terminal = "tv"
	}
	if !isSupported115CookieTerminal(terminal) {
		writeError(w, http.StatusBadRequest, "unsupported 115cookie terminal")
		return
	}

	client := pan115.New()
	session, err := client.QRCodeStart()
	if err != nil {
		a.recordProviderAuthError(r.Context(), providerModel, "115cookie", "start_qrcode", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	flow := &cookie115AuthFlow{
		ID:         newID("cookie115auth"),
		ProviderID: providerModel.ID,
		Terminal:   terminal,
		UID:        session.UID,
		PollTime:   session.Time,
		Sign:       session.Sign,
		QRCode:     session.QrcodeContent,
		State:      "pending",
		Message:    "等待扫码",
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	a.authMu.Lock()
	pruneCookie115AuthFlowsLocked(a.cookieAuthFlows)
	a.cookieAuthFlows[flow.ID] = flow
	a.authMu.Unlock()

	writeJSON(w, http.StatusOK, toCookie115AuthResponse(flow))
}

func (a *App) handleProvider115CookieAuthStatus(w http.ResponseWriter, r *http.Request, providerModel model.Provider) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id is required")
		return
	}

	flow, ok := a.getCookie115AuthFlow(sessionID, providerModel.ID)
	if !ok {
		writeError(w, http.StatusNotFound, "auth session not found")
		return
	}

	if !isCookie115AuthTerminal(flow.State) {
		if err := a.pollCookie115AuthFlow(flow); err != nil {
			a.recordProviderAuthError(r.Context(), providerModel, "115cookie", "poll_status", err)
			a.updateCookie115AuthFlow(flow.ID, func(current *cookie115AuthFlow) {
				current.State = "error"
				current.Message = err.Error()
				current.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			})
			flow, _ = a.getCookie115AuthFlow(sessionID, providerModel.ID)
		}
	}

	writeJSON(w, http.StatusOK, toCookie115AuthResponse(flow))
}

func (a *App) pollCookie115AuthFlow(flow *cookie115AuthFlow) error {
	client := pan115.New()
	status, err := client.QRCodeStatus(&pan115.QRCodeSession{
		UID:  flow.UID,
		Time: flow.PollTime,
		Sign: flow.Sign,
	})
	if err != nil {
		return err
	}

	updatedAt := time.Now().UTC().Format(time.RFC3339)
	message := strings.TrimSpace(status.Msg)

	a.updateCookie115AuthFlow(flow.ID, func(current *cookie115AuthFlow) {
		current.UpdatedAt = updatedAt
		switch {
		case status.IsExpired():
			current.State = "expired"
			current.Message = fallbackString(message, "二维码已过期")
			current.CompletedAt = updatedAt
		case status.IsCanceled():
			current.State = "cancelled"
			current.Message = fallbackString(message, "已取消授权")
			current.CompletedAt = updatedAt
		case status.IsScanned():
			current.State = "scanned"
			current.Message = fallbackString(message, "扫码成功，等待确认")
		case status.IsAllowed():
			current.State = "confirming"
			current.Message = fallbackString(message, "已确认授权，正在换取 cookie")
		default:
			current.State = "pending"
			current.Message = fallbackString(message, "等待扫码")
		}
	})

	if !status.IsAllowed() {
		return nil
	}

	credential, err := client.QRCodeLoginWithApp(&pan115.QRCodeSession{UID: flow.UID}, pan115.LoginApp(flow.Terminal))
	if err != nil {
		return err
	}
	cookieValue := credential.Cookie()
	a.persistProviderToken(flow.ProviderID, "cookie", cookieValue)

	a.updateCookie115AuthFlow(flow.ID, func(current *cookie115AuthFlow) {
		current.State = "authorized"
		current.Message = "登录成功"
		current.Cookie = cookieValue
		current.UpdatedAt = updatedAt
		current.CompletedAt = updatedAt
	})
	return nil
}

func (a *App) getCookie115AuthFlow(sessionID, providerID string) (*cookie115AuthFlow, bool) {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	pruneCookie115AuthFlowsLocked(a.cookieAuthFlows)
	flow, ok := a.cookieAuthFlows[sessionID]
	if !ok || flow.ProviderID != providerID {
		return nil, false
	}
	copy := *flow
	return &copy, true
}

func (a *App) updateCookie115AuthFlow(sessionID string, update func(flow *cookie115AuthFlow)) {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	flow, ok := a.cookieAuthFlows[sessionID]
	if !ok {
		return
	}
	update(flow)
}

func toCookie115AuthResponse(flow *cookie115AuthFlow) cookie115AuthResponse {
	if flow == nil {
		return cookie115AuthResponse{Terminals: cookie115SupportedTerminals, Recommended: cookie115RecommendedTerminals}
	}
	return cookie115AuthResponse{
		SessionID:   flow.ID,
		ProviderID:  flow.ProviderID,
		Terminal:    flow.Terminal,
		QRCode:      flow.QRCode,
		State:       flow.State,
		Message:     flow.Message,
		Cookie:      flow.Cookie,
		CreatedAt:   flow.CreatedAt,
		UpdatedAt:   flow.UpdatedAt,
		CompletedAt: flow.CompletedAt,
		Terminals:   cookie115SupportedTerminals,
		Recommended: cookie115RecommendedTerminals,
	}
}

func isSupported115CookieTerminal(value string) bool {
	for _, item := range cookie115SupportedTerminals {
		if item == value {
			return true
		}
	}
	return false
}

func isCookie115AuthTerminal(state string) bool {
	switch state {
	case "authorized", "expired", "cancelled", "error":
		return true
	default:
		return false
	}
}

func pruneCookie115AuthFlowsLocked(flows map[string]*cookie115AuthFlow) {
	cutoff := time.Now().UTC().Add(-30 * time.Minute)
	for id, flow := range flows {
		updatedAt, err := time.Parse(time.RFC3339, flow.UpdatedAt)
		if err != nil || updatedAt.Before(cutoff) {
			delete(flows, id)
		}
	}
}

package app

import (
	"net/http"
	"strings"

	"NyaMedia/internal/model"
)

type pan123CredentialsPayload struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

func (a *App) handleProvider123PanAuth(w http.ResponseWriter, r *http.Request, providerID string) {
	providerModel, err := a.providers.Get(r.Context(), providerID)
	if err != nil {
		handleStorageError(w, err)
		return
	}
	if providerModel == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	if providerModel.Type != "123pan" {
		writeError(w, http.StatusBadRequest, "provider type does not support 123pan credentials")
		return
	}
	a.handleProvider123PanCredentials(w, r, *providerModel)
}

func (a *App) handleProvider123PanCredentials(w http.ResponseWriter, r *http.Request, providerModel model.Provider) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var payload pan123CredentialsPayload
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
	a.pan123AuthMu.Lock()
	err := a.secrets.ReplaceManyAndInvalidateProviderState(
		r.Context(),
		providerModel.ID,
		credentials,
		[]string{"access_token", "access_token_expires_at"},
	)
	if err == nil {
		a.invalidatePan123Provider(providerModel.ID)
	}
	a.pan123AuthMu.Unlock()
	if err != nil {
		handleStorageError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"provider_id": providerModel.ID,
		"saved":       []string{"client_id", "client_secret"},
	})
}

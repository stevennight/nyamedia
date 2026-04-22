package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"emby115/internal/model"
	"golang.org/x/crypto/bcrypt"
)

const adminSessionCookieName = "emby115_admin_session"

type loginPayload struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authResponse struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

type updateAuthPayload struct {
	Username        string `json:"username"`
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (a *App) ensureBootstrapAdmin(ctx context.Context) error {
	if a.config.Auth.BootstrapUsername == "" || a.config.Auth.BootstrapPassword == "" {
		return nil
	}

	existing, err := a.adminUsers.GetByUsername(ctx, a.config.Auth.BootstrapUsername)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(a.config.Auth.BootstrapPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash bootstrap password: %w", err)
	}

	return a.adminUsers.Create(ctx, model.AdminUser{
		ID:           newID("admin"),
		Username:     a.config.Auth.BootstrapUsername,
		PasswordHash: string(hash),
		Role:         "admin",
		Enabled:      true,
	})
}

func (a *App) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := a.currentAdminUser(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		next(w, r)
	}
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var payload loginPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if payload.Username == "" || payload.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}

	user, err := a.adminUsers.GetByUsername(r.Context(), payload.Username)
	if err != nil {
		handleStorageError(w, err)
		return
	}
	if user == nil || !user.Enabled {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(payload.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	now := time.Now().UTC()
	expiresAt := now.Add(24 * time.Hour)
	token := newID("sess")
	if err := a.sessions.DeleteExpired(r.Context(), now.Format(time.RFC3339)); err != nil {
		handleStorageError(w, err)
		return
	}
	if err := a.sessions.Create(r.Context(), token, user.ID, expiresAt.Format(time.RFC3339)); err != nil {
		handleStorageError(w, err)
		return
	}
	if err := a.adminUsers.TouchLogin(r.Context(), user.ID, now.Format(time.RFC3339)); err != nil {
		handleStorageError(w, err)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})

	writeJSON(w, http.StatusOK, authResponse{Username: user.Username, Role: user.Role})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	cookie, err := r.Cookie(adminSessionCookieName)
	if err == nil && cookie.Value != "" {
		_ = a.sessions.Delete(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user, err := a.currentAdminUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, authResponse{Username: user.Username, Role: user.Role})
}

func (a *App) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	user, err := a.currentAdminUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	var payload updateAuthPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	nextUsername := strings.TrimSpace(payload.Username)
	newPassword := strings.TrimSpace(payload.NewPassword)
	currentPassword := payload.CurrentPassword
	if nextUsername == "" {
		nextUsername = user.Username
	}
	if nextUsername == user.Username && newPassword == "" {
		writeError(w, http.StatusBadRequest, "no account changes requested")
		return
	}
	if strings.TrimSpace(currentPassword) == "" {
		writeError(w, http.StatusBadRequest, "current_password is required")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)); err != nil {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	if len(nextUsername) < 3 {
		writeError(w, http.StatusBadRequest, "username must be at least 3 characters")
		return
	}
	if newPassword != "" && len(newPassword) < 6 {
		writeError(w, http.StatusBadRequest, "new_password must be at least 6 characters")
		return
	}
	if nextUsername != user.Username {
		existing, err := a.adminUsers.GetByUsername(r.Context(), nextUsername)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		if existing != nil && existing.ID != user.ID {
			writeError(w, http.StatusConflict, "username already exists")
			return
		}
	}

	nextPasswordHash := user.PasswordHash
	if newPassword != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("hash password: %v", err))
			return
		}
		nextPasswordHash = string(hash)
	}

	if err := a.adminUsers.UpdateCredentials(r.Context(), user.ID, nextUsername, nextPasswordHash); err != nil {
		handleStorageError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, authResponse{Username: nextUsername, Role: user.Role})
}

func (a *App) currentAdminUser(r *http.Request) (*model.AdminUser, error) {
	cookie, err := r.Cookie(adminSessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, fmt.Errorf("authentication required")
	}

	now := time.Now().UTC()
	_ = a.sessions.DeleteExpired(r.Context(), now.Format(time.RFC3339))
	session, err := a.sessions.Get(r.Context(), cookie.Value)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("authentication required")
	}
	expiresAt, err := time.Parse(time.RFC3339, session.ExpiresAt)
	if err != nil || !expiresAt.After(now) {
		_ = a.sessions.Delete(r.Context(), cookie.Value)
		return nil, fmt.Errorf("session expired")
	}

	user, err := a.adminUsers.GetByID(r.Context(), session.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil || !user.Enabled {
		return nil, fmt.Errorf("authentication required")
	}
	_ = a.sessions.Touch(r.Context(), session.Token, now.Format(time.RFC3339))
	return user, nil
}

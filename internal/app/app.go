package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"emby115/internal/config"
	"emby115/internal/model"
	"emby115/internal/storage"
)

type App struct {
	config     config.Config
	db         *sql.DB
	httpServer *http.Server
	providers  *storage.ProviderRepository
	libraries  *storage.LibraryRepository
	settings   *storage.SettingRepository
}

func New(cfg config.Config) (*App, error) {
	db, err := storage.OpenSQLite(cfg.Storage.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if err := storage.RunMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	app := &App{
		config:    cfg,
		db:        db,
		providers: storage.NewProviderRepository(db),
		libraries: storage.NewLibraryRepository(db),
		settings:  storage.NewSettingRepository(db),
	}
	app.httpServer = &http.Server{
		Addr:              cfg.Server.Address(),
		Handler:           app.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		log.Printf("http server listening on %s", a.config.Server.Address())
		if err := a.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return a.httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (a *App) Close() error {
	if a.db == nil {
		return nil
	}
	return a.db.Close()
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", a.handleHealth)
	mux.HandleFunc("/api/v1/system/info", a.handleSystemInfo)
	mux.HandleFunc("/api/v1/providers", a.handleProviders)
	mux.HandleFunc("/api/v1/providers/", a.handleProviderByID)
	mux.HandleFunc("/api/v1/libraries", a.handleLibraries)
	mux.HandleFunc("/api/v1/libraries/", a.handleLibraryRoutes)
	mux.HandleFunc("/api/v1/settings", a.handleSettings)
	mux.HandleFunc("/api/v1/settings/", a.handleSettingByKey)
	return mux
}

func (a *App) handleHealth(w http.ResponseWriter, _ *http.Request) {
	if err := a.db.Ping(); err != nil {
		http.Error(w, fmt.Sprintf(`{"status":"error","error":%q}`, err.Error()), http.StatusServiceUnavailable)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
	})
}

func (a *App) handleSystemInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":            "emby115",
		"public_base_url": a.config.Server.PublicBaseURL,
		"database_path":   a.config.Storage.DBPath,
		"strm_output_dir": a.config.Storage.STRMOutputDir,
	})
}

type providerPayload struct {
	ID          string               `json:"id"`
	Type        string               `json:"type"`
	Name        string               `json:"name"`
	RootPath    string               `json:"root_path"`
	Status      model.ProviderStatus `json:"status"`
	LastCheckAt string               `json:"last_check_at,omitempty"`
	LastError   string               `json:"last_error,omitempty"`
	Config      json.RawMessage      `json:"config,omitempty"`
	Enabled     bool                 `json:"enabled"`
}

func (a *App) handleProviders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := a.providers.List(r.Context())
		if err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var payload providerPayload
		if err := decodeJSON(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		provider, err := toProviderModel(payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		if err := a.providers.Create(r.Context(), provider); err != nil {
			handleStorageError(w, err)
			return
		}

		created, err := a.providers.Get(r.Context(), provider.ID)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleProviderByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/providers/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		item, err := a.providers.Get(r.Context(), id)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		if item == nil {
			writeError(w, http.StatusNotFound, "resource not found")
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPut:
		var payload providerPayload
		if err := decodeJSON(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		payload.ID = id
		provider, err := toProviderModel(payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.providers.Update(r.Context(), provider); err != nil {
			handleStorageError(w, err)
			return
		}
		item, err := a.providers.Get(r.Context(), id)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if err := a.providers.Delete(r.Context(), id); err != nil {
			handleStorageError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

type libraryPayload struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	LastScanAt  string `json:"last_scan_at,omitempty"`
}

type libraryMountPayload struct {
	ID         string `json:"id"`
	ProviderID string `json:"provider_id"`
	SourcePath string `json:"source_path"`
	TargetPath string `json:"target_path"`
	MediaType  string `json:"media_type,omitempty"`
	Priority   int    `json:"priority"`
	Enabled    bool   `json:"enabled"`
}

func (a *App) handleLibraries(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := a.libraries.List(r.Context())
		if err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var payload libraryPayload
		if err := decodeJSON(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		item, err := toLibraryModel(payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.libraries.Create(r.Context(), item); err != nil {
			handleStorageError(w, err)
			return
		}
		created, err := a.libraries.Get(r.Context(), item.ID)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleLibraryRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/libraries/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}

	id := parts[0]
	if len(parts) == 1 {
		a.handleLibraryByID(w, r, id)
		return
	}

	if len(parts) == 2 && parts[1] == "mounts" {
		a.handleLibraryMounts(w, r, id)
		return
	}
	if len(parts) == 3 && parts[1] == "mounts" {
		a.handleLibraryMountByID(w, r, id, parts[2])
		return
	}

	writeError(w, http.StatusNotFound, "resource not found")
}

func (a *App) handleLibraryByID(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		item, err := a.libraries.Get(r.Context(), id)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		if item == nil {
			writeError(w, http.StatusNotFound, "resource not found")
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPut:
		var payload libraryPayload
		if err := decodeJSON(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		payload.ID = id
		item, err := toLibraryModel(payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.libraries.Update(r.Context(), item); err != nil {
			handleStorageError(w, err)
			return
		}
		updated, err := a.libraries.Get(r.Context(), id)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		if err := a.libraries.Delete(r.Context(), id); err != nil {
			handleStorageError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleLibraryMounts(w http.ResponseWriter, r *http.Request, libraryID string) {
	switch r.Method {
	case http.MethodGet:
		items, err := a.libraries.ListMounts(r.Context(), libraryID)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var payload libraryMountPayload
		if err := decodeJSON(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		item, err := toLibraryMountModel(libraryID, payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.libraries.CreateMount(r.Context(), item); err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleLibraryMountByID(w http.ResponseWriter, r *http.Request, libraryID, mountID string) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := a.libraries.DeleteMount(r.Context(), libraryID, mountID); err != nil {
		handleStorageError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type settingPayload struct {
	Value any `json:"value"`
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	items, err := a.settings.List(r.Context())
	if err != nil {
		handleStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleSettingByKey(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/api/v1/settings/")
	if key == "" || strings.Contains(key, "/") {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		item, err := a.settings.Get(r.Context(), key)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		if item == nil {
			writeError(w, http.StatusNotFound, "resource not found")
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPut:
		var payload settingPayload
		if err := decodeJSON(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		encoded, err := json.Marshal(payload.Value)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("encode setting value: %v", err))
			return
		}
		if err := a.settings.Upsert(r.Context(), model.Setting{Key: key, ValueJSON: string(encoded)}); err != nil {
			handleStorageError(w, err)
			return
		}
		item, err := a.settings.Get(r.Context(), key)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if err := a.settings.Delete(r.Context(), key); err != nil {
			handleStorageError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func toProviderModel(payload providerPayload) (model.Provider, error) {
	if payload.ID == "" {
		return model.Provider{}, fmt.Errorf("id is required")
	}
	if payload.Type == "" {
		return model.Provider{}, fmt.Errorf("type is required")
	}
	if payload.Name == "" {
		return model.Provider{}, fmt.Errorf("name is required")
	}
	if payload.RootPath == "" {
		return model.Provider{}, fmt.Errorf("root_path is required")
	}
	configJSON := ""
	if len(payload.Config) > 0 {
		configJSON = string(payload.Config)
	}
	return model.Provider{
		ID:          payload.ID,
		Type:        payload.Type,
		Name:        payload.Name,
		RootPath:    payload.RootPath,
		Status:      payload.Status,
		LastCheckAt: payload.LastCheckAt,
		LastError:   payload.LastError,
		ConfigJSON:  configJSON,
		Enabled:     payload.Enabled,
	}, nil
}

func toLibraryModel(payload libraryPayload) (model.Library, error) {
	if payload.ID == "" {
		return model.Library{}, fmt.Errorf("id is required")
	}
	if payload.Name == "" {
		return model.Library{}, fmt.Errorf("name is required")
	}
	return model.Library{
		ID:          payload.ID,
		Name:        payload.Name,
		Description: payload.Description,
		Enabled:     payload.Enabled,
		LastScanAt:  payload.LastScanAt,
	}, nil
}

func toLibraryMountModel(libraryID string, payload libraryMountPayload) (model.LibraryMount, error) {
	if payload.ID == "" {
		return model.LibraryMount{}, fmt.Errorf("id is required")
	}
	if payload.ProviderID == "" {
		return model.LibraryMount{}, fmt.Errorf("provider_id is required")
	}
	if payload.SourcePath == "" {
		return model.LibraryMount{}, fmt.Errorf("source_path is required")
	}
	if payload.TargetPath == "" {
		return model.LibraryMount{}, fmt.Errorf("target_path is required")
	}
	if payload.Priority == 0 {
		payload.Priority = 100
	}
	return model.LibraryMount{
		ID:         payload.ID,
		LibraryID:  libraryID,
		ProviderID: payload.ProviderID,
		SourcePath: payload.SourcePath,
		TargetPath: payload.TargetPath,
		MediaType:  payload.MediaType,
		Priority:   payload.Priority,
		Enabled:    payload.Enabled,
	}, nil
}

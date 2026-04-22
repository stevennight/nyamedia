package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"emby115/internal/config"
	"emby115/internal/model"
	provideriface "emby115/internal/provider"
	localprovider "emby115/internal/provider/local"
	"emby115/internal/storage"
	"emby115/internal/web"
)

type App struct {
	config     config.Config
	db         *sql.DB
	httpServer *http.Server
	providers  *storage.ProviderRepository
	secrets    *storage.ProviderSecretRepository
	libraries  *storage.LibraryRepository
	settings   *storage.SettingRepository
	tasks      *storage.ScanTaskRepository
	entries    *storage.EntryRepository
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
		secrets:   storage.NewProviderSecretRepository(db),
		libraries: storage.NewLibraryRepository(db),
		settings:  storage.NewSettingRepository(db),
		tasks:     storage.NewScanTaskRepository(db),
		entries:   storage.NewEntryRepository(db),
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
	mux.HandleFunc("/", a.handleAdminIndex)
	mux.Handle("/admin/static/", http.StripPrefix("/admin/static/", http.FileServer(http.FS(mustSubFS("static")))))
	mux.HandleFunc("/healthz", a.handleHealth)
	mux.HandleFunc("/stream/", a.handleStream)
	mux.HandleFunc("/api/v1/system/info", a.handleSystemInfo)
	mux.HandleFunc("/api/v1/providers", a.handleProviders)
	mux.HandleFunc("/api/v1/providers/", a.handleProviderRoutes)
	mux.HandleFunc("/api/v1/libraries", a.handleLibraries)
	mux.HandleFunc("/api/v1/libraries/", a.handleLibraryRoutes)
	mux.HandleFunc("/api/v1/settings", a.handleSettings)
	mux.HandleFunc("/api/v1/settings/", a.handleSettingByKey)
	mux.HandleFunc("/api/v1/tasks", a.handleTasks)
	mux.HandleFunc("/api/v1/tasks/", a.handleTaskByID)
	mux.HandleFunc("/api/v1/entries", a.handleEntries)
	mux.HandleFunc("/api/v1/scan/full", a.handleScanFull)
	mux.HandleFunc("/api/v1/scan/library/", a.handleScanLibrary)
	return mux
}

func mustSubFS(dir string) fs.FS {
	sub, err := fs.Sub(web.Assets, "static")
	if err != nil {
		panic(err)
	}
	return sub
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

func (a *App) handleAdminIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/admin" && r.URL.Path != "/admin/" {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	data, err := web.Assets.ReadFile("static/index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

var mediaExtensions = map[string]struct{}{
	".mkv":  {},
	".mp4":  {},
	".avi":  {},
	".ts":   {},
	".m2ts": {},
	".mov":  {},
	".wmv":  {},
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

func (a *App) handleProviderRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/providers/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}

	id := parts[0]
	if len(parts) == 1 {
		a.handleProviderByID(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "secrets" {
		a.handleProviderSecrets(w, r, id)
		return
	}
	if len(parts) == 3 && parts[1] == "secrets" {
		a.handleProviderSecretByType(w, r, id, parts[2])
		return
	}

	writeError(w, http.StatusNotFound, "resource not found")
}

func (a *App) handleProviderByID(w http.ResponseWriter, r *http.Request, id string) {
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

type providerSecretPayload struct {
	Value string `json:"value"`
}

type providerSecretResponse struct {
	ProviderID  string `json:"provider_id"`
	SecretType  string `json:"secret_type"`
	MaskedValue string `json:"masked_value"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

func (a *App) handleProviderSecrets(w http.ResponseWriter, r *http.Request, providerID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	items, err := a.secrets.ListByProvider(r.Context(), providerID)
	if err != nil {
		handleStorageError(w, err)
		return
	}

	responses := make([]providerSecretResponse, 0, len(items))
	for _, item := range items {
		responses = append(responses, providerSecretResponse{
			ProviderID:  item.ProviderID,
			SecretType:  item.SecretType,
			MaskedValue: item.MaskedValue,
			UpdatedAt:   item.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": responses})
}

func (a *App) handleProviderSecretByType(w http.ResponseWriter, r *http.Request, providerID, secretType string) {
	switch r.Method {
	case http.MethodGet:
		item, err := a.secrets.Get(r.Context(), providerID, secretType)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		if item == nil {
			writeError(w, http.StatusNotFound, "resource not found")
			return
		}
		writeJSON(w, http.StatusOK, providerSecretResponse{ProviderID: item.ProviderID, SecretType: item.SecretType, MaskedValue: item.MaskedValue, UpdatedAt: item.UpdatedAt})
	case http.MethodPut:
		var payload providerSecretPayload
		if err := decodeJSON(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if payload.Value == "" {
			writeError(w, http.StatusBadRequest, "value is required")
			return
		}
		item := model.ProviderSecret{ProviderID: providerID, SecretType: secretType, SecretValue: payload.Value, MaskedValue: maskSecret(payload.Value)}
		if err := a.secrets.Upsert(r.Context(), item); err != nil {
			handleStorageError(w, err)
			return
		}
		stored, err := a.secrets.Get(r.Context(), providerID, secretType)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, providerSecretResponse{ProviderID: stored.ProviderID, SecretType: stored.SecretType, MaskedValue: stored.MaskedValue, UpdatedAt: stored.UpdatedAt})
	case http.MethodDelete:
		if err := a.secrets.Delete(r.Context(), providerID, secretType); err != nil {
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

func (a *App) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	items, err := a.tasks.List(r.Context())
	if err != nil {
		handleStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleEntries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	providerID := strings.TrimSpace(r.URL.Query().Get("provider_id"))
	prefix := strings.TrimSpace(r.URL.Query().Get("prefix"))
	limit := 200
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		if _, err := fmt.Sscanf(rawLimit, "%d", &limit); err != nil || limit <= 0 || limit > 1000 {
			writeError(w, http.StatusBadRequest, "limit must be an integer between 1 and 1000")
			return
		}
	}
	items, err := a.entries.List(r.Context(), providerID, prefix, limit)
	if err != nil {
		handleStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleTaskByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	item, err := a.tasks.Get(r.Context(), id)
	if err != nil {
		handleStorageError(w, err)
		return
	}
	if item == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *App) handleScanFull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	task, err := a.createScanTask(r.Context(), model.ScanTask{TaskType: "full_scan", Status: model.TaskStatusPending, Message: "queued full scan task"})
	if err != nil {
		handleStorageError(w, err)
		return
	}
	go a.runFullScan(task.ID)
	writeJSON(w, http.StatusCreated, task)
}

func (a *App) handleScanLibrary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	libraryID := strings.TrimPrefix(r.URL.Path, "/api/v1/scan/library/")
	if libraryID == "" || strings.Contains(libraryID, "/") {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	library, err := a.libraries.Get(r.Context(), libraryID)
	if err != nil {
		handleStorageError(w, err)
		return
	}
	if library == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	task, err := a.createScanTask(r.Context(), model.ScanTask{TaskType: "library_scan", LibraryID: libraryID, Status: model.TaskStatusPending, Message: "queued library scan task"})
	if err != nil {
		handleStorageError(w, err)
		return
	}
	go a.runLibraryScanTask(task.ID, libraryID)
	writeJSON(w, http.StatusCreated, task)
}

func (a *App) createScanTask(ctx context.Context, task model.ScanTask) (*model.ScanTask, error) {
	task.ID = newID("task")
	now := time.Now().UTC().Format(time.RFC3339)
	task.StartedAt = now
	if err := a.tasks.Create(ctx, task); err != nil {
		return nil, err
	}
	return a.tasks.Get(ctx, task.ID)
}

func (a *App) runFullScan(taskID string) {
	ctx := context.Background()
	libraries, err := a.libraries.ListEnabled(ctx)
	if err != nil {
		a.failTask(ctx, taskID, err)
		return
	}

	task, err := a.tasks.Get(ctx, taskID)
	if err != nil || task == nil {
		return
	}
	task.Status = model.TaskStatusRunning
	task.ProgressTotal = len(libraries)
	task.ProgressDone = 0
	task.Message = "running full scan"
	if err := a.tasks.Update(ctx, *task); err != nil {
		return
	}

	for idx, library := range libraries {
		if err := a.scanLibrary(ctx, library.ID); err != nil {
			a.failTask(ctx, taskID, err)
			return
		}
		task.ProgressDone = idx + 1
		if err := a.tasks.Update(ctx, *task); err != nil {
			return
		}
	}

	task.Status = model.TaskStatusCompleted
	task.Message = "full scan completed"
	task.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	_ = a.tasks.Update(ctx, *task)
}

func (a *App) runLibraryScanTask(taskID, libraryID string) {
	ctx := context.Background()
	task, err := a.tasks.Get(ctx, taskID)
	if err != nil || task == nil {
		return
	}
	task.Status = model.TaskStatusRunning
	task.ProgressTotal = 1
	task.ProgressDone = 0
	task.Message = "running library scan"
	if err := a.tasks.Update(ctx, *task); err != nil {
		return
	}

	if err := a.scanLibrary(ctx, libraryID); err != nil {
		a.failTask(ctx, taskID, err)
		return
	}

	task.Status = model.TaskStatusCompleted
	task.ProgressDone = 1
	task.Message = "library scan completed"
	task.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	_ = a.tasks.Update(ctx, *task)
}

func (a *App) failTask(ctx context.Context, taskID string, runErr error) {
	task, err := a.tasks.Get(ctx, taskID)
	if err != nil || task == nil {
		return
	}
	task.Status = model.TaskStatusFailed
	task.ErrorMessage = runErr.Error()
	task.Message = "scan failed"
	task.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	_ = a.tasks.Update(ctx, *task)
}

func (a *App) scanLibrary(ctx context.Context, libraryID string) error {
	mounts, err := a.libraries.ListEnabledMounts(ctx, libraryID)
	if err != nil {
		return err
	}

	for _, mount := range mounts {
		if err := a.scanMount(ctx, mount); err != nil {
			return fmt.Errorf("scan mount %s: %w", mount.ID, err)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	return a.libraries.MarkScanned(ctx, libraryID, now)
}

func (a *App) scanMount(ctx context.Context, mount model.LibraryMount) error {
	providerModel, err := a.providers.Get(ctx, mount.ProviderID)
	if err != nil {
		return err
	}
	if providerModel == nil {
		return fmt.Errorf("provider %s not found", mount.ProviderID)
	}
	if providerModel.Type != "local" {
		return fmt.Errorf("provider type %s not implemented yet", providerModel.Type)
	}

	provider := localprovider.New(providerModel.ID, providerModel.RootPath)
	seenAt := time.Now().UTC().Format(time.RFC3339)

	if err := provider.WalkFiles(ctx, mount.SourcePath, func(entry provideriface.Entry) error {
		modelEntry := model.Entry{
			ID:         newID("entry"),
			ProviderID: providerModel.ID,
			EntryType:  "file",
			Path:       entry.Path,
			ParentPath: path.Dir(entry.Path),
			Name:       entry.Name,
			Size:       entry.Size,
			MTime:      entry.ModTime,
			MimeType:   entry.MimeType,
			LastSeenAt: seenAt,
		}
		if modelEntry.ParentPath == "." {
			modelEntry.ParentPath = "/"
		}
		if err := a.entries.Upsert(ctx, modelEntry); err != nil {
			return err
		}
		if !isMediaFile(entry.Name) {
			return nil
		}
		return a.writeSTRM(providerModel.ID, mount, entry.Path)
	}); err != nil {
		return err
	}

	return a.entries.DeleteStaleUnderPrefix(ctx, providerModel.ID, normalizeProviderPath(mount.SourcePath), seenAt)
}

func (a *App) writeSTRM(providerID string, mount model.LibraryMount, providerPath string) error {
	relToMount := strings.TrimPrefix(normalizeProviderPath(providerPath), normalizeProviderPath(mount.SourcePath))
	relToMount = strings.TrimPrefix(relToMount, "/")
	base := strings.TrimSuffix(relToMount, filepath.Ext(relToMount)) + ".strm"

	targetRoot := filepath.Join(a.config.Storage.STRMOutputDir, filepath.FromSlash(strings.TrimPrefix(mount.TargetPath, "/")))
	outPath := filepath.Join(targetRoot, filepath.FromSlash(base))
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("create strm dir: %w", err)
	}

	streamURL := strings.TrimRight(a.config.Server.PublicBaseURL, "/") + "/stream/" + providerID + escapeProviderPath(providerPath)
	if err := os.WriteFile(outPath, []byte(streamURL), 0o644); err != nil {
		return fmt.Errorf("write strm file %s: %w", outPath, err)
	}
	return nil
}

func (a *App) handleStream(w http.ResponseWriter, r *http.Request) {
	pathValue := strings.TrimPrefix(r.URL.Path, "/stream/")
	parts := strings.SplitN(pathValue, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}

	providerID := parts[0]
	providerPath, err := decodeProviderPath(parts[1])
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	if providerModel.Type != "local" {
		writeError(w, http.StatusNotImplemented, "provider stream not implemented")
		return
	}

	provider := localprovider.New(providerModel.ID, providerModel.RootPath)
	filePath, err := provider.ResolveFilePath(providerPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	http.ServeFile(w, r, filePath)
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

func maskSecret(value string) string {
	if len(value) <= 4 {
		return strings.Repeat("*", len(value))
	}
	if len(value) <= 8 {
		return value[:1] + strings.Repeat("*", len(value)-2) + value[len(value)-1:]
	}
	return value[:2] + strings.Repeat("*", len(value)-4) + value[len(value)-2:]
}

func isMediaFile(name string) bool {
	_, ok := mediaExtensions[strings.ToLower(filepath.Ext(name))]
	return ok
}

func normalizeProviderPath(value string) string {
	clean := path.Clean("/" + strings.TrimSpace(value))
	if clean == "." {
		return "/"
	}
	return clean
}

func escapeProviderPath(value string) string {
	segments := strings.Split(strings.TrimPrefix(normalizeProviderPath(value), "/"), "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return "/" + strings.Join(segments, "/")
}

func decodeProviderPath(value string) (string, error) {
	segments := strings.Split(value, "/")
	decoded := make([]string, 0, len(segments))
	for _, segment := range segments {
		item, err := url.PathUnescape(segment)
		if err != nil {
			return "", fmt.Errorf("decode provider path: %w", err)
		}
		decoded = append(decoded, item)
	}
	return "/" + strings.Join(decoded, "/"), nil
}

func newID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(buf)
}

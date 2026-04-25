package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"emby115/internal/config"
	"emby115/internal/model"
	provideriface "emby115/internal/provider"
	cookie115provider "emby115/internal/provider/cookie115"
	localprovider "emby115/internal/provider/local"
	open115provider "emby115/internal/provider/open115"
	"emby115/internal/storage"
	"emby115/internal/web"
)

type App struct {
	config          config.Config
	db              *sql.DB
	httpServer      *http.Server
	providers       *storage.ProviderRepository
	embyServers     *storage.EmbyServerRepository
	adminUsers      *storage.AdminUserRepository
	sessions        *storage.AdminSessionRepository
	secrets         *storage.ProviderSecretRepository
	libraries       *storage.LibraryRepository
	settings        *storage.SettingRepository
	tasks           *storage.ScanTaskRepository
	taskLogs        *storage.TaskLogRepository
	events          *storage.SystemEventRepository
	entries         *storage.EntryRepository
	watchMu         sync.Mutex
	watchTimers     map[string]*time.Timer
	watchStatus     map[string]providerWatchStatus
	authMu          sync.Mutex
	authFlows       map[string]*open115AuthFlow
	cookieAuthFlows map[string]*cookie115AuthFlow
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
		config:          cfg,
		db:              db,
		providers:       storage.NewProviderRepository(db),
		embyServers:     storage.NewEmbyServerRepository(db),
		adminUsers:      storage.NewAdminUserRepository(db),
		sessions:        storage.NewAdminSessionRepository(db),
		secrets:         storage.NewProviderSecretRepository(db),
		libraries:       storage.NewLibraryRepository(db),
		settings:        storage.NewSettingRepository(db),
		tasks:           storage.NewScanTaskRepository(db),
		taskLogs:        storage.NewTaskLogRepository(db),
		events:          storage.NewSystemEventRepository(db),
		entries:         storage.NewEntryRepository(db),
		watchTimers:     make(map[string]*time.Timer),
		watchStatus:     make(map[string]providerWatchStatus),
		authFlows:       make(map[string]*open115AuthFlow),
		cookieAuthFlows: make(map[string]*cookie115AuthFlow),
	}
	if err := app.ensureBootstrapAdmin(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ensure bootstrap admin: %w", err)
	}
	if err := app.recoverInterruptedScanTasks(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("recover interrupted scan tasks: %w", err)
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
	watchCtx, stopWatchers := context.WithCancel(context.Background())
	defer stopWatchers()

	a.startProviderWatchers(watchCtx)

	go func() {
		log.Printf("http server listening on %s", a.config.Server.Address())
		if err := a.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		stopWatchers()
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
	mux.Handle("/admin/static/", http.StripPrefix("/admin/static/", http.FileServer(http.FS(mustSubFS()))))
	mux.HandleFunc("/healthz", a.handleHealth)
	mux.HandleFunc("/proxy/", a.handleEmbyProxy)
	mux.HandleFunc("/stream/", a.handleStream)
	mux.HandleFunc("/api/v1/auth/login", a.handleLogin)
	mux.HandleFunc("/api/v1/auth/logout", a.requireAdmin(a.handleLogout))
	mux.HandleFunc("/api/v1/auth/me", a.requireAdmin(a.handleMe))
	mux.HandleFunc("/api/v1/auth/me/account", a.requireAdmin(a.handleUpdateMe))
	mux.HandleFunc("/api/v1/system/info", a.requireAdmin(a.handleSystemInfo))
	mux.HandleFunc("/api/v1/system/events", a.requireAdmin(a.handleSystemEvents))
	mux.HandleFunc("/api/v1/providers", a.requireAdmin(a.handleProviders))
	mux.HandleFunc("/api/v1/providers/", a.requireAdmin(a.handleProviderRoutes))
	mux.HandleFunc("/api/v1/emby-servers", a.requireAdmin(a.handleEmbyServers))
	mux.HandleFunc("/api/v1/emby-servers/", a.requireAdmin(a.handleEmbyServerRoutes))
	mux.HandleFunc("/api/v1/libraries", a.requireAdmin(a.handleLibraries))
	mux.HandleFunc("/api/v1/libraries/", a.requireAdmin(a.handleLibraryRoutes))
	mux.HandleFunc("/api/v1/settings", a.requireAdmin(a.handleSettings))
	mux.HandleFunc("/api/v1/settings/", a.requireAdmin(a.handleSettingByKey))
	mux.HandleFunc("/api/v1/tasks", a.requireAdmin(a.handleTasks))
	mux.HandleFunc("/api/v1/tasks/", a.requireAdmin(a.handleTaskRoutes))
	mux.HandleFunc("/api/v1/entries", a.requireAdmin(a.handleEntries))
	mux.HandleFunc("/api/v1/scan/full", a.requireAdmin(a.handleScanFull))
	mux.HandleFunc("/api/v1/scan/library/", a.requireAdmin(a.handleScanLibrary))
	return mux
}

func mustSubFS() fs.FS {
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
	if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/admin") {
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
	ID           string               `json:"id"`
	Type         string               `json:"type"`
	Name         string               `json:"name"`
	RootPath     string               `json:"root_path"`
	Status       model.ProviderStatus `json:"status"`
	LastCheckAt  string               `json:"last_check_at,omitempty"`
	LastError    string               `json:"last_error,omitempty"`
	Config       json.RawMessage      `json:"config,omitempty"`
	Enabled      bool                 `json:"enabled"`
	WatchEnabled bool                 `json:"watch_enabled"`
}

type providerConfig struct {
	Downloads *providerDownloadSettings `json:"downloads,omitempty"`
}

type providerDownloadSettings struct {
	STRM      *bool `json:"strm,omitempty"`
	NFO       *bool `json:"nfo,omitempty"`
	Images    *bool `json:"images,omitempty"`
	Subtitles *bool `json:"subtitles,omitempty"`
	BIF       *bool `json:"bif,omitempty"`
	MediaInfo *bool `json:"mediainfo,omitempty"`
}

type providerDownloadOptions struct {
	STRM      bool
	NFO       bool
	Images    bool
	Subtitles bool
	BIF       bool
	MediaInfo bool
}

type outputSyncJob struct {
	Kind       string
	SourcePath string
	TargetPath string
}

type downloadProgressFunc func(message string, payload map[string]any)

type mediaOutputPaths struct {
	TargetRoot string
	TargetDir  string
	BaseName   string
}

var subtitleExtensions = map[string]struct{}{
	".srt": {},
	".ass": {},
	".ssa": {},
	".sub": {},
	".idx": {},
	".vtt": {},
	".sup": {},
}

var imageExtensions = map[string]struct{}{
	".jpg":  {},
	".jpeg": {},
	".png":  {},
	".webp": {},
	".avif": {},
}

var artworkBaseNames = map[string]struct{}{
	"poster":    {},
	"fanart":    {},
	"backdrop":  {},
	"folder":    {},
	"thumb":     {},
	"landscape": {},
	"clearlogo": {},
	"clearart":  {},
	"disc":      {},
	"discart":   {},
	"logo":      {},
	"banner":    {},
}

func (a *App) handleProviders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := a.providers.List(r.Context())
		if err != nil {
			handleStorageError(w, err)
			return
		}
		responses := make([]providerPayload, 0, len(items))
		for _, item := range items {
			responses = append(responses, a.toProviderResponse(r.Context(), item))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": responses})
	case http.MethodPost:
		var payload providerPayload
		if err := decodeJSON(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		payload.ID = newUUID()

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
		writeJSON(w, http.StatusCreated, a.toProviderResponse(r.Context(), *created))
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
	if len(parts) == 2 && parts[1] == "watch" {
		a.handleProviderWatch(w, r, id)
		return
	}
	if len(parts) == 3 && parts[1] == "auth" && parts[2] == "115open" {
		a.handleProvider115OpenAuth(w, r, id)
		return
	}
	if len(parts) == 3 && parts[1] == "auth" && parts[2] == "115cookie" {
		a.handleProvider115CookieAuth(w, r, id)
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
		writeJSON(w, http.StatusOK, a.toProviderResponse(r.Context(), *item))
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
		writeJSON(w, http.StatusOK, a.toProviderResponse(r.Context(), *item))
	case http.MethodDelete:
		mountCount, err := a.libraries.CountMountsByProvider(r.Context(), id)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		if mountCount > 0 {
			writeError(w, http.StatusConflict, fmt.Sprintf("provider is referenced by %d mount(s) and cannot be deleted", mountCount))
			return
		}
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

type scanLibraryPayload struct {
	SourcePath string `json:"source_path,omitempty"`
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
	switch r.Method {
	case http.MethodPut:
		var payload libraryMountPayload
		if err := decodeJSON(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		payload.ID = mountID
		item, err := toLibraryMountModel(libraryID, payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.libraries.UpdateMount(r.Context(), item); err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if err := a.libraries.DeleteMount(r.Context(), libraryID, mountID); err != nil {
			handleStorageError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
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

type providerWatchResponse struct {
	ProviderID string                `json:"provider_id"`
	Capable    bool                  `json:"capable"`
	Mounts     []providerWatchStatus `json:"mounts"`
}

func (a *App) handleProviderWatch(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	providerModel, err := a.providers.Get(r.Context(), id)
	if err != nil {
		handleStorageError(w, err)
		return
	}
	if providerModel == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}

	writeJSON(w, http.StatusOK, providerWatchResponse{
		ProviderID: id,
		Capable:    a.providerWatchCapable(providerModel),
		Mounts:     a.listWatchStatusByProvider(id),
	})
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

func (a *App) handleTaskRoutes(w http.ResponseWriter, r *http.Request) {
	pathValue := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
	parts := strings.Split(pathValue, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	if len(parts) == 1 {
		a.handleTaskByID(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "logs" {
		a.handleTaskLogs(w, r, parts[0])
		return
	}
	writeError(w, http.StatusNotFound, "resource not found")
}

func (a *App) handleSystemEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	limit := 100
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		if _, err := fmt.Sscanf(rawLimit, "%d", &limit); err != nil || limit <= 0 || limit > 1000 {
			writeError(w, http.StatusBadRequest, "limit must be an integer between 1 and 1000")
			return
		}
	}

	items, err := a.events.List(r.Context(), limit)
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
	page := 1
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		if _, err := fmt.Sscanf(rawLimit, "%d", &limit); err != nil || limit <= 0 || limit > 1000 {
			writeError(w, http.StatusBadRequest, "limit must be an integer between 1 and 1000")
			return
		}
	}
	if rawPage := strings.TrimSpace(r.URL.Query().Get("page")); rawPage != "" {
		if _, err := fmt.Sscanf(rawPage, "%d", &page); err != nil || page <= 0 {
			writeError(w, http.StatusBadRequest, "page must be a positive integer")
			return
		}
	}
	offset := (page - 1) * limit
	items, total, err := a.entries.ListPage(r.Context(), providerID, prefix, limit, offset)
	if err != nil {
		handleStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"pagination": map[string]any{
			"page":  page,
			"limit": limit,
			"total": total,
		},
	})
}

func (a *App) handleTaskByID(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
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

func (a *App) handleTaskLogs(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := 500
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		if _, err := fmt.Sscanf(rawLimit, "%d", &limit); err != nil || limit <= 0 || limit > 2000 {
			writeError(w, http.StatusBadRequest, "limit must be an integer between 1 and 2000")
			return
		}
	}
	beforeCreatedAt := strings.TrimSpace(r.URL.Query().Get("before_created_at"))
	beforeID := strings.TrimSpace(r.URL.Query().Get("before_id"))
	afterCreatedAt := strings.TrimSpace(r.URL.Query().Get("after_created_at"))
	afterID := strings.TrimSpace(r.URL.Query().Get("after_id"))
	if (beforeCreatedAt == "") != (beforeID == "") {
		writeError(w, http.StatusBadRequest, "before_created_at and before_id must be provided together")
		return
	}
	if (afterCreatedAt == "") != (afterID == "") {
		writeError(w, http.StatusBadRequest, "after_created_at and after_id must be provided together")
		return
	}
	if beforeCreatedAt != "" && afterCreatedAt != "" {
		writeError(w, http.StatusBadRequest, "before and after cursors cannot be combined")
		return
	}
	items, hasMore, err := a.taskLogs.ListByTask(r.Context(), taskID, storage.TaskLogListOptions{
		Limit:           limit,
		BeforeCreatedAt: beforeCreatedAt,
		BeforeID:        beforeID,
		AfterCreatedAt:  afterCreatedAt,
		AfterID:         afterID,
	})
	if err != nil {
		handleStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "has_more": hasMore})
}

func (a *App) handleScanFull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	active, err := a.tasks.FindActive(r.Context(), "full_scan", "")
	if err != nil {
		handleStorageError(w, err)
		return
	}
	if active != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "full scan already running", "task": active})
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
	var payload scanLibraryPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	active, err := a.tasks.FindActive(r.Context(), "library_scan", libraryID)
	if err != nil {
		handleStorageError(w, err)
		return
	}
	if active != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "library scan already running", "task": active})
		return
	}
	message := "queued library scan task"
	if strings.TrimSpace(payload.SourcePath) != "" {
		message = "queued partial library scan task"
	}
	task, err := a.createScanTask(r.Context(), model.ScanTask{TaskType: "library_scan", LibraryID: libraryID, Status: model.TaskStatusPending, Message: message})
	if err != nil {
		handleStorageError(w, err)
		return
	}
	go a.runLibraryScanTask(task.ID, libraryID, payload.SourcePath)
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

func (a *App) appendTaskLog(ctx context.Context, taskID, level, message string, payload any) {
	payloadJSON := ""
	if payload != nil {
		if encoded, err := json.Marshal(payload); err == nil {
			payloadJSON = string(encoded)
		}
	}
	_ = a.taskLogs.Create(ctx, model.TaskLog{
		ID:          newID("tasklog"),
		TaskID:      taskID,
		Level:       level,
		Message:     message,
		PayloadJSON: payloadJSON,
	})
}

func (a *App) recoverInterruptedScanTasks(ctx context.Context) error {
	items, err := a.tasks.List(ctx)
	if err != nil {
		return err
	}
	finishedAt := time.Now().UTC().Format(time.RFC3339)
	for _, item := range items {
		if item.Status != model.TaskStatusPending && item.Status != model.TaskStatusRunning {
			continue
		}
		previousStatus := item.Status
		item.Status = model.TaskStatusFailed
		item.Message = "scan interrupted by service restart"
		if item.ErrorMessage == "" {
			item.ErrorMessage = "task did not resume after service restart"
		}
		item.FinishedAt = finishedAt
		if err := a.tasks.Update(ctx, item); err != nil {
			return err
		}
		a.appendTaskLog(ctx, item.ID, "error", "scan interrupted by service restart", map[string]any{"previous_status": previousStatus})
	}
	return nil
}

func (a *App) runFullScan(taskID string) {
	ctx := context.Background()
	a.appendTaskLog(ctx, taskID, "info", "starting full scan", nil)
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
		a.appendTaskLog(ctx, taskID, "info", "scanning library", map[string]any{"library_id": library.ID})
		if err := a.scanLibrary(ctx, taskID, library.ID, ""); err != nil {
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
	a.appendTaskLog(ctx, taskID, "info", "full scan completed", map[string]any{"libraries": len(libraries)})
	_ = a.tasks.Update(ctx, *task)
}

func (a *App) runLibraryScanTask(taskID, libraryID, sourcePath string) {
	ctx := context.Background()
	normalizedSourcePath := ""
	if strings.TrimSpace(sourcePath) != "" {
		normalizedSourcePath = normalizeProviderPath(sourcePath)
	}
	a.appendTaskLog(ctx, taskID, "info", "starting library scan", map[string]any{"library_id": libraryID, "source_path": normalizedSourcePath})
	task, err := a.tasks.Get(ctx, taskID)
	if err != nil || task == nil {
		return
	}
	task.Status = model.TaskStatusRunning
	task.ProgressTotal = 1
	task.ProgressDone = 0
	if normalizedSourcePath == "" {
		task.Message = "running library scan"
	} else {
		task.Message = "running partial library scan"
	}
	if err := a.tasks.Update(ctx, *task); err != nil {
		return
	}

	if err := a.scanLibrary(ctx, taskID, libraryID, normalizedSourcePath); err != nil {
		a.failTask(ctx, taskID, err)
		return
	}

	task.Status = model.TaskStatusCompleted
	task.ProgressDone = 1
	task.Message = "library scan completed"
	task.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	a.appendTaskLog(ctx, taskID, "info", "library scan completed", map[string]any{"library_id": libraryID, "source_path": normalizedSourcePath})
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
	a.appendTaskLog(ctx, taskID, "error", "scan failed", map[string]any{"error": runErr.Error()})
	_ = a.tasks.Update(ctx, *task)
}

func (a *App) scanLibrary(ctx context.Context, taskID, libraryID, sourcePath string) error {
	mounts, err := a.libraries.ListEnabledMounts(ctx, libraryID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(sourcePath) != "" {
		sourcePath = normalizeProviderPath(sourcePath)
		mount, ok := findMountForSourcePath(mounts, sourcePath)
		if !ok {
			return fmt.Errorf("source path %s is not under an enabled mount for library %s", sourcePath, libraryID)
		}
		targetRoot, expectedSTRM, err := a.scanMount(ctx, taskID, mount, sourcePath)
		if err != nil {
			return fmt.Errorf("scan mount %s: %w", mount.ID, err)
		}
		deletedCount, err := cleanupStaleSTRM(targetRoot, expectedSTRM)
		if err != nil {
			return err
		}
		if taskID != "" {
			a.appendTaskLog(ctx, taskID, "info", "partial library target cleanup completed", map[string]any{"target_root": targetRoot, "source_path": sourcePath, "deleted": deletedCount})
		}

		now := time.Now().UTC().Format(time.RFC3339)
		return a.libraries.MarkScanned(ctx, libraryID, now)
	}

	rootExpected := make(map[string]map[string]struct{})
	for _, mount := range mounts {
		targetRoot, expectedSTRM, err := a.scanMount(ctx, taskID, mount, mount.SourcePath)
		if err != nil {
			return fmt.Errorf("scan mount %s: %w", mount.ID, err)
		}
		merged := rootExpected[targetRoot]
		if merged == nil {
			merged = make(map[string]struct{}, len(expectedSTRM))
			rootExpected[targetRoot] = merged
		}
		for path := range expectedSTRM {
			merged[path] = struct{}{}
		}
	}

	for targetRoot := range rootExpected {
		expectedForRoot := make(map[string]struct{})
		for root, expected := range rootExpected {
			if !pathWithinRoot(root, targetRoot) {
				continue
			}
			for outPath := range expected {
				expectedForRoot[outPath] = struct{}{}
			}
		}
		deletedCount, err := cleanupStaleSTRM(targetRoot, expectedForRoot)
		if err != nil {
			return err
		}
		if taskID != "" {
			a.appendTaskLog(ctx, taskID, "info", "library target cleanup completed", map[string]any{"target_root": targetRoot, "deleted": deletedCount})
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	return a.libraries.MarkScanned(ctx, libraryID, now)
}

func (a *App) scanMount(ctx context.Context, taskID string, mount model.LibraryMount, scanSourcePath string) (string, map[string]struct{}, error) {
	providerModel, err := a.providers.Get(ctx, mount.ProviderID)
	if err != nil {
		return "", nil, err
	}
	if providerModel == nil {
		return "", nil, fmt.Errorf("provider %s not found", mount.ProviderID)
	}
	runtimeProvider, ok, err := a.buildProvider(*providerModel)
	if err != nil {
		return "", nil, err
	}
	if !ok {
		return "", nil, fmt.Errorf("provider type %s not implemented yet", providerModel.Type)
	}
	provider, ok := runtimeProvider.(provideriface.ScanProvider)
	if !ok {
		return "", nil, fmt.Errorf("provider type %s does not support scanning", providerModel.Type)
	}
	downloads := providerDownloadOptionsFor(*providerModel)
	seenAt := time.Now().UTC().Format(time.RFC3339)
	scanSourcePath = normalizeProviderPath(scanSourcePath)
	if !providerPathWithinRoot(scanSourcePath, mount.SourcePath) {
		return "", nil, fmt.Errorf("scan source path %s is outside mount source path %s", scanSourcePath, mount.SourcePath)
	}
	targetRoot := a.mountTargetDirForProviderDir(mount, scanSourcePath)
	expectedSTRM := make(map[string]struct{})
	filesByDir := make(map[string][]provideriface.Entry)
	mediaEntries := make([]provideriface.Entry, 0)
	if taskID != "" {
		a.appendTaskLog(ctx, taskID, "info", "scanning mount", map[string]any{"mount_id": mount.ID, "provider_id": mount.ProviderID, "source_path": mount.SourcePath, "scan_source_path": scanSourcePath, "target_path": mount.TargetPath, "downloads": downloads})
	}

	if err := provider.WalkFiles(ctx, scanSourcePath, func(entry provideriface.Entry) error {
		entryType := "file"
		if entry.IsDir {
			entryType = "dir"
		}
		modelEntry := model.Entry{
			ID:              newID("entry"),
			ProviderID:      providerModel.ID,
			EntryType:       entryType,
			Path:            entry.Path,
			ParentPath:      path.Dir(entry.Path),
			Name:            entry.Name,
			Size:            entry.Size,
			MTime:           entry.ModTime,
			MimeType:        entry.MimeType,
			ProviderEntryID: entry.ID,
			MetadataJSON:    entryMetadataJSON(entry.Metadata),
			LastSeenAt:      seenAt,
		}
		if modelEntry.ParentPath == "." {
			modelEntry.ParentPath = "/"
		}
		if err := a.entries.Upsert(ctx, modelEntry); err != nil {
			return err
		}
		dirPath := path.Dir(entry.Path)
		if dirPath == "." {
			dirPath = "/"
		}
		if !entry.IsDir {
			filesByDir[dirPath] = append(filesByDir[dirPath], entry)
		}
		if !entry.IsDir && isMediaFile(entry.Name) {
			mediaEntries = append(mediaEntries, entry)
		}
		return nil
	}); err != nil {
		return "", nil, err
	}

	syncedOutputs := make(map[string]struct{})
	for _, mediaEntry := range mediaEntries {
		if downloads.STRM {
			outPath, err := a.writeSTRM(providerModel.ID, mount, mediaEntry.Path)
			if err != nil {
				return "", nil, err
			}
			expectedSTRM[outPath] = struct{}{}
			if taskID != "" {
				a.appendTaskLog(ctx, taskID, "info", "generated strm", map[string]any{"provider_path": mediaEntry.Path, "output_path": outPath})
			}
		}

		dirPath := path.Dir(mediaEntry.Path)
		if dirPath == "." {
			dirPath = "/"
		}
		jobs := a.buildOutputSyncJobs(mount, mediaEntry, filesByDir[dirPath], downloads)
		for _, job := range jobs {
			if _, exists := syncedOutputs[job.TargetPath]; exists {
				continue
			}
			if taskID != "" {
				a.appendTaskLog(ctx, taskID, "info", fmt.Sprintf("start sync %s", job.Kind), map[string]any{"provider_path": job.SourcePath, "output_path": job.TargetPath})
			}
			progress := func(message string, payload map[string]any) {
				if taskID == "" {
					return
				}
				base := map[string]any{"provider_path": job.SourcePath, "output_path": job.TargetPath}
				for key, value := range payload {
					base[key] = value
				}
				a.appendTaskLog(ctx, taskID, "info", message, base)
			}
			if err := a.downloadProviderFile(ctx, runtimeProvider, job.SourcePath, job.TargetPath, progress); err != nil {
				return "", nil, fmt.Errorf("sync %s %s: %w", job.Kind, job.SourcePath, err)
			}
			syncedOutputs[job.TargetPath] = struct{}{}
			if taskID != "" {
				a.appendTaskLog(ctx, taskID, "info", fmt.Sprintf("downloaded %s", job.Kind), map[string]any{"provider_path": job.SourcePath, "output_path": job.TargetPath})
			}
		}
	}

	if taskID != "" {
		a.appendTaskLog(ctx, taskID, "info", "mount scan completed", map[string]any{"mount_id": mount.ID, "generated_strm": len(expectedSTRM), "downloaded_files": len(syncedOutputs), "target_root": targetRoot})
	}

	if err := a.entries.DeleteStaleUnderPrefix(ctx, providerModel.ID, scanSourcePath, seenAt); err != nil {
		return "", nil, err
	}
	return targetRoot, expectedSTRM, nil
}

func (a *App) writeSTRM(providerID string, mount model.LibraryMount, providerPath string) (string, error) {
	paths := a.mediaOutputPaths(mount, providerPath)
	outPath := filepath.Join(paths.TargetDir, paths.BaseName+".strm")
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", fmt.Errorf("create strm dir: %w", err)
	}

	streamURL := strings.TrimRight(a.config.Server.PublicBaseURL, "/") + "/stream/" + providerID + escapeProviderPath(providerPath)
	if err := os.WriteFile(outPath, []byte(streamURL), 0o644); err != nil {
		return "", fmt.Errorf("write strm file %s: %w", outPath, err)
	}
	return filepath.Clean(outPath), nil
}

func entryMetadataJSON(metadata map[string]string) string {
	if len(metadata) == 0 {
		return ""
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func (a *App) downloadProviderFile(ctx context.Context, runtimeProvider provideriface.Provider, providerPath, targetPath string, progress downloadProgressFunc) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	tmpPath := targetPath + ".part"
	if provider, ok := runtimeProvider.(provideriface.LocalFileProvider); ok {
		if progress != nil {
			progress("resolve local file", nil)
		}
		sourcePath, err := provider.ResolveFilePath(providerPath)
		if err != nil {
			return err
		}
		if progress != nil {
			progress("copy local file started", map[string]any{"source_path": sourcePath})
		}
		source, err := os.Open(sourcePath)
		if err != nil {
			return fmt.Errorf("open local file %s: %w", sourcePath, err)
		}
		defer source.Close()
		target, err := os.Create(tmpPath)
		if err != nil {
			return fmt.Errorf("create target file %s: %w", tmpPath, err)
		}
		_, copyErr := io.Copy(target, source)
		closeErr := target.Close()
		if copyErr != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("copy local file %s: %w", sourcePath, copyErr)
		}
		if closeErr != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("close target file %s: %w", tmpPath, closeErr)
		}
		if err := os.Rename(tmpPath, targetPath); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("replace target file %s: %w", targetPath, err)
		}
		if progress != nil {
			progress("copy local file finished", nil)
		}
		return nil
	}

	if progress != nil {
		progress("request direct link", nil)
	}
	directLink, err := runtimeProvider.GetDirectLink(ctx, providerPath)
	if err != nil {
		return err
	}
	if directLink == nil || directLink.URL == "" {
		return fmt.Errorf("provider returned empty direct link")
	}
	if progress != nil {
		progress("direct link ready", map[string]any{"direct_link_url": directLink.URL})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, directLink.URL, nil)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}
	for key, value := range directLink.Headers {
		req.Header.Set(key, value)
	}
	if progress != nil {
		progress("download started", map[string]any{"request_url": directLink.URL})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download file: unexpected status %s", resp.Status)
	}
	target, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create target file %s: %w", tmpPath, err)
	}
	_, copyErr := io.Copy(target, resp.Body)
	closeErr := target.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write target file %s: %w", tmpPath, copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close target file %s: %w", tmpPath, closeErr)
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace target file %s: %w", targetPath, err)
	}
	if progress != nil {
		progress("download finished", map[string]any{"status": resp.Status, "content_length": resp.ContentLength})
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
	runtimeProvider, ok, err := a.buildProvider(*providerModel)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotImplemented, "provider stream not implemented")
		return
	}
	if provider, ok := runtimeProvider.(provideriface.LocalFileProvider); ok {
		filePath, err := provider.ResolveFilePath(providerPath)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		http.ServeFile(w, r, filePath)
		return
	}

	ctx := provideriface.WithRequestUserAgent(r.Context(), r.Header.Get("User-Agent"))
	a.loadPersistedEntryMetadata(ctx, runtimeProvider, providerID, providerPath)
	directLink, err := runtimeProvider.GetDirectLink(ctx, providerPath)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if directLink == nil || directLink.URL == "" {
		writeError(w, http.StatusBadGateway, "provider returned empty direct link")
		return
	}
	if len(directLink.Headers) > 0 {
		log.Printf("stream direct link headers provider=%s path=%s headers=%v", providerID, providerPath, directLink.Headers)
	}

	mode := model.PlaybackModeRedirect
	//if strings.EqualFold(r.URL.Query().Get("mode"), string(model.PlaybackModeProxy)) || len(directLink.Headers) > 0 {
	if strings.EqualFold(r.URL.Query().Get("mode"), string(model.PlaybackModeProxy)) {
		mode = model.PlaybackModeProxy
	}
	if mode == model.PlaybackModeProxy {
		a.proxyDirectLink(w, r, directLink)
		return
	}
	http.Redirect(w, r, directLink.URL, http.StatusTemporaryRedirect)
}

func (a *App) loadPersistedEntryMetadata(ctx context.Context, runtimeProvider provideriface.Provider, providerID, providerPath string) {
	metadataProvider, ok := runtimeProvider.(provideriface.PersistedEntryMetadataProvider)
	if !ok {
		return
	}
	entry, err := a.entries.Get(ctx, providerID, providerPath)
	if err != nil {
		log.Printf("load persisted entry metadata provider=%s path=%s: %v", providerID, providerPath, err)
		return
	}
	if entry == nil || (entry.ProviderEntryID == "" && strings.TrimSpace(entry.MetadataJSON) == "") {
		return
	}
	metadata := make(map[string]string)
	if strings.TrimSpace(entry.MetadataJSON) != "" {
		if err := json.Unmarshal([]byte(entry.MetadataJSON), &metadata); err != nil {
			log.Printf("parse persisted entry metadata provider=%s path=%s: %v", providerID, providerPath, err)
			metadata = make(map[string]string)
		}
	}
	metadataProvider.LoadPersistedEntryMetadata(providerPath, entry.ProviderEntryID, metadata)
}

func (a *App) proxyDirectLink(w http.ResponseWriter, r *http.Request, directLink *provideriface.DirectLinkResult) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, directLink.URL, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("build upstream request: %v", err))
		return
	}

	copyRequestHeaders(req.Header, r.Header)
	for key, value := range directLink.Headers {
		if strings.TrimSpace(key) == "" || value == "" {
			continue
		}
		req.Header.Set(key, value)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("proxy upstream request: %v", err))
		return
	}
	defer resp.Body.Close()

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("proxy direct link body copy failed: %v", err)
	}
}

func copyRequestHeaders(dst, src http.Header) {
	for _, key := range []string{"Accept", "Accept-Encoding", "If-Modified-Since", "If-None-Match", "If-Range", "Range", "User-Agent"} {
		values := src.Values(key)
		if len(values) == 0 {
			continue
		}
		dst[key] = append([]string(nil), values...)
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		switch http.CanonicalHeaderKey(key) {
		case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
			continue
		}
		dst[key] = append([]string(nil), values...)
	}
	if _, ok := dst["Accept-Ranges"]; !ok {
		dst["Accept-Ranges"] = []string{"bytes"}
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
	if payload.Type == "115open" || payload.Type == "115cookie" {
		payload.WatchEnabled = false
	}
	configJSON := ""
	if len(payload.Config) > 0 {
		configJSON = string(payload.Config)
	}
	return model.Provider{
		ID:           payload.ID,
		Type:         payload.Type,
		Name:         payload.Name,
		RootPath:     payload.RootPath,
		Status:       payload.Status,
		LastCheckAt:  payload.LastCheckAt,
		LastError:    payload.LastError,
		ConfigJSON:   configJSON,
		Enabled:      payload.Enabled,
		WatchEnabled: payload.WatchEnabled,
	}, nil
}

func toProviderPayload(provider model.Provider) providerPayload {
	var config json.RawMessage
	if provider.ConfigJSON != "" {
		config = json.RawMessage(provider.ConfigJSON)
	}

	return providerPayload{
		ID:           provider.ID,
		Type:         provider.Type,
		Name:         provider.Name,
		RootPath:     provider.RootPath,
		Status:       provider.Status,
		LastCheckAt:  provider.LastCheckAt,
		LastError:    provider.LastError,
		Config:       config,
		Enabled:      provider.Enabled,
		WatchEnabled: provider.WatchEnabled,
	}
}

func (a *App) toProviderResponse(ctx context.Context, provider model.Provider) providerPayload {
	payload := toProviderPayload(provider)
	status, lastError, checked := a.checkProviderStatus(ctx, provider)
	if checked {
		payload.Status = status
		payload.LastError = lastError
		payload.LastCheckAt = time.Now().UTC().Format(time.RFC3339)
	}
	return payload
}

func (a *App) checkProviderStatus(ctx context.Context, provider model.Provider) (model.ProviderStatus, string, bool) {
	if !provider.Enabled {
		return model.ProviderStatusDisabled, "", true
	}

	runtimeProvider, ok, err := a.buildProvider(provider)
	if err != nil {
		return model.ProviderStatusError, err.Error(), true
	}
	if !ok {
		return model.ProviderStatusUnknown, "", false
	}

	statusProvider, ok := runtimeProvider.(provideriface.StatusProvider)
	if !ok {
		return model.ProviderStatusUnknown, "", false
	}

	status, lastError := statusProvider.CheckStatus(ctx)
	return status, lastError, true
}

func (a *App) buildProvider(providerModel model.Provider) (provideriface.Provider, bool, error) {
	switch providerModel.Type {
	case "local":
		return localprovider.New(providerModel.ID, providerModel.RootPath), true, nil
	case "115cookie":
		secrets, err := a.loadProviderSecretValues(context.Background(), providerModel.ID)
		if err != nil {
			return nil, false, err
		}
		cookieValue := strings.TrimSpace(secrets["cookie"])
		if cookieValue == "" {
			return nil, false, fmt.Errorf("provider secret cookie is required")
		}
		provider, err := cookie115provider.New(providerModel.ID, providerModel.RootPath, cookieValue, secrets["user_agent"])
		if err != nil {
			return nil, false, err
		}
		return provider, true, nil
	case "115open":
		secrets, err := a.loadProviderSecretValues(context.Background(), providerModel.ID)
		if err != nil {
			return nil, false, err
		}
		return open115provider.New(
			providerModel.ID,
			providerModel.RootPath,
			secrets["access_token"],
			secrets["refresh_token"],
			func(accessToken, refreshToken string) {
				a.persistProviderToken(providerModel.ID, "access_token", accessToken)
				a.persistProviderToken(providerModel.ID, "refresh_token", refreshToken)
			},
		), true, nil
	default:
		return nil, false, nil
	}
}

func (a *App) loadProviderSecretValues(ctx context.Context, providerID string) (map[string]string, error) {
	items, err := a.secrets.ListByProvider(ctx, providerID)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(items))
	for _, item := range items {
		values[item.SecretType] = item.SecretValue
	}
	return values, nil
}

func (a *App) persistProviderToken(providerID, secretType, secretValue string) {
	secretValue = strings.TrimSpace(secretValue)
	if secretValue == "" {
		return
	}
	if err := a.secrets.Upsert(context.Background(), model.ProviderSecret{
		ProviderID:  providerID,
		SecretType:  secretType,
		SecretValue: secretValue,
		MaskedValue: maskSecret(secretValue),
	}); err != nil {
		log.Printf("persist provider token %s/%s: %v", providerID, secretType, err)
	}
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

func providerDownloadOptionsFor(provider model.Provider) providerDownloadOptions {
	options := providerDownloadOptions{
		STRM:      true,
		NFO:       true,
		Images:    true,
		Subtitles: true,
		BIF:       true,
		MediaInfo: true,
	}
	if strings.TrimSpace(provider.ConfigJSON) == "" {
		return options
	}
	var cfg providerConfig
	if err := json.Unmarshal([]byte(provider.ConfigJSON), &cfg); err != nil || cfg.Downloads == nil {
		return options
	}
	if cfg.Downloads.STRM != nil {
		options.STRM = *cfg.Downloads.STRM
	}
	if cfg.Downloads.NFO != nil {
		options.NFO = *cfg.Downloads.NFO
	}
	if cfg.Downloads.Images != nil {
		options.Images = *cfg.Downloads.Images
	}
	if cfg.Downloads.Subtitles != nil {
		options.Subtitles = *cfg.Downloads.Subtitles
	}
	if cfg.Downloads.BIF != nil {
		options.BIF = *cfg.Downloads.BIF
	}
	if cfg.Downloads.MediaInfo != nil {
		options.MediaInfo = *cfg.Downloads.MediaInfo
	}
	return options
}

func (a *App) buildOutputSyncJobs(mount model.LibraryMount, mediaEntry provideriface.Entry, dirEntries []provideriface.Entry, downloads providerDownloadOptions) []outputSyncJob {
	paths := a.mediaOutputPaths(mount, mediaEntry.Path)
	baseNameLower := strings.ToLower(paths.BaseName)
	jobs := make([]outputSyncJob, 0)
	for _, entry := range dirEntries {
		if entry.Path == mediaEntry.Path {
			continue
		}
		job, ok := classifyOutputSyncJob(entry, paths, baseNameLower, downloads)
		if !ok {
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs
}

func classifyOutputSyncJob(entry provideriface.Entry, paths mediaOutputPaths, baseNameLower string, downloads providerDownloadOptions) (outputSyncJob, bool) {
	nameLower := strings.ToLower(entry.Name)
	if downloads.NFO && nameLower == baseNameLower+".nfo" {
		return outputSyncJob{Kind: "nfo", SourcePath: entry.Path, TargetPath: filepath.Join(paths.TargetDir, entry.Name)}, true
	}
	if downloads.Subtitles && isSubtitleSidecar(baseNameLower, nameLower) {
		return outputSyncJob{Kind: "subtitle", SourcePath: entry.Path, TargetPath: filepath.Join(paths.TargetDir, entry.Name)}, true
	}
	if downloads.BIF && isBIFSidecar(baseNameLower, nameLower) {
		return outputSyncJob{Kind: "bif", SourcePath: entry.Path, TargetPath: filepath.Join(paths.TargetDir, entry.Name)}, true
	}
	if downloads.MediaInfo && isMediaInfoSidecar(baseNameLower, nameLower) {
		return outputSyncJob{Kind: "mediainfo", SourcePath: entry.Path, TargetPath: filepath.Join(paths.TargetDir, entry.Name)}, true
	}
	if downloads.Images && isImageSidecar(baseNameLower, nameLower) {
		return outputSyncJob{Kind: "image", SourcePath: entry.Path, TargetPath: filepath.Join(paths.TargetDir, entry.Name)}, true
	}
	return outputSyncJob{}, false
}

func isSubtitleSidecar(baseNameLower, nameLower string) bool {
	ext := strings.ToLower(filepath.Ext(nameLower))
	if _, ok := subtitleExtensions[ext]; !ok {
		return false
	}
	stem := strings.TrimSuffix(nameLower, ext)
	return stem == baseNameLower || strings.HasPrefix(stem, baseNameLower+".")
}

func isBIFSidecar(baseNameLower, nameLower string) bool {
	if strings.ToLower(filepath.Ext(nameLower)) != ".bif" {
		return false
	}
	stem := strings.TrimSuffix(nameLower, ".bif")
	return stem == baseNameLower || stem == "index"
}

func isMediaInfoSidecar(baseNameLower, nameLower string) bool {
	if nameLower == "mediainfo.json" {
		return true
	}
	return nameLower == baseNameLower+".mediainfo.json" || nameLower == baseNameLower+"-mediainfo.json"
}

func isImageSidecar(baseNameLower, nameLower string) bool {
	ext := strings.ToLower(filepath.Ext(nameLower))
	if _, ok := imageExtensions[ext]; !ok {
		return false
	}
	stem := strings.TrimSuffix(nameLower, ext)
	if stem == baseNameLower || strings.HasPrefix(stem, baseNameLower+".") || strings.HasPrefix(stem, baseNameLower+"-") {
		return true
	}
	_, ok := artworkBaseNames[stem]
	return ok
}

func (a *App) mediaOutputPaths(mount model.LibraryMount, providerPath string) mediaOutputPaths {
	relToMount := strings.TrimPrefix(normalizeProviderPath(providerPath), normalizeProviderPath(mount.SourcePath))
	relToMount = strings.TrimPrefix(relToMount, "/")
	relDir := filepath.Dir(filepath.FromSlash(relToMount))
	baseName := strings.TrimSuffix(filepath.Base(relToMount), filepath.Ext(relToMount))
	targetRoot := filepath.Join(a.config.Storage.STRMOutputDir, filepath.FromSlash(strings.TrimPrefix(mount.TargetPath, "/")))
	targetDir := targetRoot
	if relDir != "." {
		targetDir = filepath.Join(targetRoot, relDir)
	}
	return mediaOutputPaths{
		TargetRoot: filepath.Clean(targetRoot),
		TargetDir:  filepath.Clean(targetDir),
		BaseName:   baseName,
	}
}

func (a *App) mountTargetDirForProviderDir(mount model.LibraryMount, providerDir string) string {
	relToMount := strings.TrimPrefix(normalizeProviderPath(providerDir), normalizeProviderPath(mount.SourcePath))
	relToMount = strings.TrimPrefix(relToMount, "/")
	targetRoot := filepath.Join(a.config.Storage.STRMOutputDir, filepath.FromSlash(strings.TrimPrefix(mount.TargetPath, "/")))
	if relToMount == "" {
		return filepath.Clean(targetRoot)
	}
	return filepath.Clean(filepath.Join(targetRoot, filepath.FromSlash(relToMount)))
}

func findMountForSourcePath(mounts []model.LibraryMount, sourcePath string) (model.LibraryMount, bool) {
	var selected model.LibraryMount
	selectedLen := -1
	for _, mount := range mounts {
		mountSourcePath := normalizeProviderPath(mount.SourcePath)
		if !providerPathWithinRoot(sourcePath, mountSourcePath) {
			continue
		}
		if len(mountSourcePath) > selectedLen {
			selected = mount
			selectedLen = len(mountSourcePath)
		}
	}
	return selected, selectedLen >= 0
}

func providerPathWithinRoot(candidatePath, rootPath string) bool {
	cleanCandidate := normalizeProviderPath(candidatePath)
	cleanRoot := normalizeProviderPath(rootPath)
	if cleanRoot == "/" || cleanCandidate == cleanRoot {
		return true
	}
	return strings.HasPrefix(cleanCandidate, cleanRoot+"/")
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

func cleanupStaleSTRM(targetRoot string, expected map[string]struct{}) (int, error) {
	if _, err := os.Stat(targetRoot); os.IsNotExist(err) {
		return 0, nil
	} else if err != nil {
		return 0, fmt.Errorf("stat strm root %s: %w", targetRoot, err)
	}

	deleted := 0
	var dirs []string
	err := filepath.Walk(targetRoot, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			dirs = append(dirs, current)
			return nil
		}
		if strings.ToLower(filepath.Ext(current)) != ".strm" {
			return nil
		}
		clean := filepath.Clean(current)
		if _, ok := expected[clean]; ok {
			return nil
		}
		if err := os.Remove(clean); err != nil {
			return fmt.Errorf("remove stale strm %s: %w", clean, err)
		}
		deleted++
		return nil
	})
	if err != nil {
		return 0, err
	}

	for i := len(dirs) - 1; i >= 0; i-- {
		if dirs[i] == targetRoot {
			continue
		}
		_ = os.Remove(dirs[i])
	}

	return deleted, nil
}

func pathWithinRoot(candidateRoot, baseRoot string) bool {
	cleanCandidate := filepath.Clean(candidateRoot)
	cleanBase := filepath.Clean(baseRoot)
	if cleanCandidate == cleanBase {
		return true
	}
	return strings.HasPrefix(cleanCandidate, cleanBase+string(filepath.Separator))
}

func newID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(buf)
}

func newUUID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	}

	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80

	return hex.EncodeToString(buf[0:4]) + "-" +
		hex.EncodeToString(buf[4:6]) + "-" +
		hex.EncodeToString(buf[6:8]) + "-" +
		hex.EncodeToString(buf[8:10]) + "-" +
		hex.EncodeToString(buf[10:16])
}

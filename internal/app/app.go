package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"NyaMedia/internal/config"
	"NyaMedia/internal/model"
	provideriface "NyaMedia/internal/provider"
	cookie115provider "NyaMedia/internal/provider/cookie115"
	localprovider "NyaMedia/internal/provider/local"
	open115provider "NyaMedia/internal/provider/open115"
	"NyaMedia/internal/storage"
	"NyaMedia/internal/web"
)

type App struct {
	config          config.Config
	db              *sql.DB
	httpServer      *http.Server
	providers       *storage.ProviderRepository
	providerCache   *storage.ProviderCacheRepository
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
	activeTaskMu    sync.Mutex
	activeTasks     map[string]context.CancelFunc
	watchMu         sync.Mutex
	watchTimers     map[string]*time.Timer
	watchStatus     map[string]providerWatchStatus
	scheduleMu      sync.Mutex
	scheduledScans  map[string]string
	authMu          sync.Mutex
	authFlows       map[string]*open115AuthFlow
	cookieAuthFlows map[string]*cookie115AuthFlow
}

const (
	maxConcurrentLibraryScans = 2
	scanProgressLogInterval   = 500
	providerDownloadAttempts  = 3
)

func New(cfg config.Config) (*App, error) {
	db, err := storage.OpenPostgres(cfg.Storage.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	if err := storage.RunMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	app := &App{
		config:          cfg,
		db:              db,
		providers:       storage.NewProviderRepository(db),
		providerCache:   storage.NewProviderCacheRepository(db),
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
		activeTasks:     make(map[string]context.CancelFunc),
		watchTimers:     make(map[string]*time.Timer),
		watchStatus:     make(map[string]providerWatchStatus),
		scheduledScans:  make(map[string]string),
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
	scheduleCtx, stopScheduler := context.WithCancel(context.Background())
	defer stopScheduler()
	cacheCtx, stopCachePruner := context.WithCancel(context.Background())
	defer stopCachePruner()
	logPrunerCtx, stopLogPruner := context.WithCancel(context.Background())
	defer stopLogPruner()

	a.startProviderWatchers(watchCtx)
	go a.startLibraryScanScheduler(scheduleCtx)
	go a.startProviderCachePruner(cacheCtx)
	go a.startScanLogPruner(logPrunerCtx)

	go func() {
		log.Printf("http server listening on %s", a.config.Server.Address())
		if err := a.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		stopWatchers()
		stopScheduler()
		stopCachePruner()
		stopLogPruner()
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
	mux.HandleFunc("/api/v1/webhooks/filesystem", a.handleFilesystemWebhook)
	mux.HandleFunc("/api/v1/webhooks/clouddrive2", a.handleFilesystemWebhook)
	mux.HandleFunc("/api/v1/auth/logout", a.requireAdmin(a.handleLogout))
	mux.HandleFunc("/api/v1/auth/me", a.requireAdmin(a.handleMe))
	mux.HandleFunc("/api/v1/auth/me/account", a.requireAdmin(a.handleUpdateMe))
	mux.HandleFunc("/api/v1/system/info", a.requireAdmin(a.handleSystemInfo))
	mux.HandleFunc("/api/v1/dashboard/summary", a.requireAdmin(a.handleDashboardSummary))
	mux.HandleFunc("/api/v1/system/events", a.requireAdmin(a.handleSystemEvents))
	mux.HandleFunc("/api/v1/filesystem/directories", a.requireAdmin(a.handleFilesystemDirectories))
	mux.HandleFunc("/api/v1/filesystem/output-directories", a.requireAdmin(a.handleOutputDirectories))
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

func (a *App) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	systemLocation, systemTimezone := a.systemLocation(r.Context())
	systemNow := now.In(systemLocation)
	zoneName, zoneOffset := now.Zone()
	systemZoneName, systemZoneOffset := systemNow.Zone()
	writeJSON(w, http.StatusOK, map[string]any{
		"name":              "NyaMedia",
		"public_base_url":   a.config.Server.PublicBaseURL,
		"database_url":      a.config.Storage.MaskedDatabaseURL(),
		"strm_output_dir":   a.config.Storage.STRMOutputDir,
		"server_time":       now.Format(time.RFC3339),
		"server_timezone":   zoneName,
		"server_utc_offset": formatUTCOffset(zoneOffset),
		"system_timezone":   systemTimezone,
		"system_time":       systemNow.Format(time.RFC3339),
		"system_zone_name":  systemZoneName,
		"system_utc_offset": formatUTCOffset(systemZoneOffset),
	})
}

func (a *App) handleFilesystemDirectories(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.handleListFilesystemDirectories(w, r)
	case http.MethodPost:
		a.handleCreateFilesystemDirectory(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleListFilesystemDirectories(w http.ResponseWriter, r *http.Request) {
	dirPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if dirPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		dirPath = cwd
	}

	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	absPath = filepath.Clean(absPath)

	info, err := os.Stat(absPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, "path is not a directory")
		return
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	directories := make([]filesystemDirectoryItem, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		directories = append(directories, filesystemDirectoryItem{Name: name, Path: filepath.Join(absPath, name)})
	}
	sort.Slice(directories, func(i, j int) bool {
		return strings.ToLower(directories[i].Name) < strings.ToLower(directories[j].Name)
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"path":        absPath,
		"parent_path": filesystemParentPath(absPath),
		"roots":       filesystemRoots(),
		"items":       directories,
	})
}

func (a *App) handleCreateFilesystemDirectory(w http.ResponseWriter, r *http.Request) {
	var payload filesystemDirectoryPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	parentPath := strings.TrimSpace(payload.Path)
	name := strings.TrimSpace(payload.Name)
	if parentPath == "" || name == "" {
		writeError(w, http.StatusBadRequest, "path and name are required")
		return
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		writeError(w, http.StatusBadRequest, "directory name is invalid")
		return
	}

	parentAbsPath, err := filepath.Abs(parentPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	parentAbsPath = filepath.Clean(parentAbsPath)
	info, err := os.Stat(parentAbsPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, "path is not a directory")
		return
	}

	dirPath := filepath.Join(parentAbsPath, name)
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, filesystemDirectoryItem{Name: name, Path: dirPath})
}

func (a *App) handleOutputDirectories(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.handleListOutputDirectories(w, r)
	case http.MethodPost:
		a.handleCreateOutputDirectory(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleListOutputDirectories(w http.ResponseWriter, r *http.Request) {
	virtualPath := normalizeProviderPath(r.URL.Query().Get("path"))
	dirPath, err := a.outputDirectoryPath(virtualPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	info, err := os.Stat(dirPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, "path is not a directory")
		return
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	directories := make([]filesystemDirectoryItem, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		directories = append(directories, filesystemDirectoryItem{Name: name, Path: normalizeProviderPath(path.Join(virtualPath, name))})
	}
	sort.Slice(directories, func(i, j int) bool {
		return strings.ToLower(directories[i].Name) < strings.ToLower(directories[j].Name)
	})

	parentPath := ""
	if virtualPath != "/" {
		parentPath = normalizeProviderPath(path.Dir(virtualPath))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":        virtualPath,
		"parent_path": parentPath,
		"output_root": filepath.Clean(a.config.Storage.STRMOutputDir),
		"items":       directories,
	})
}

func (a *App) handleCreateOutputDirectory(w http.ResponseWriter, r *http.Request) {
	var payload filesystemDirectoryPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	parentPath := normalizeProviderPath(payload.Path)
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		writeError(w, http.StatusBadRequest, "directory name is invalid")
		return
	}

	parentDirPath, err := a.outputDirectoryPath(parentPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := os.Stat(parentDirPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, "path is not a directory")
		return
	}

	createdPath := normalizeProviderPath(path.Join(parentPath, name))
	createdDirPath, err := a.outputDirectoryPath(createdPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.Mkdir(createdDirPath, 0o755); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, filesystemDirectoryItem{Name: name, Path: createdPath})
}

func (a *App) outputDirectoryPath(virtualPath string) (string, error) {
	outputRoot := filepath.Clean(a.config.Storage.STRMOutputDir)
	dirPath := filepath.Clean(filepath.Join(outputRoot, filepath.FromSlash(strings.TrimPrefix(normalizeProviderPath(virtualPath), "/"))))
	if !pathWithinRoot(dirPath, outputRoot) {
		return "", fmt.Errorf("path is outside strm output dir")
	}
	return dirPath, nil
}

func filesystemParentPath(dirPath string) string {
	parent := filepath.Dir(dirPath)
	if parent == dirPath {
		return ""
	}
	if runtime.GOOS == "windows" && strings.EqualFold(parent, dirPath) {
		return ""
	}
	return parent
}

func filesystemRoots() []string {
	if runtime.GOOS != "windows" {
		return []string{string(filepath.Separator)}
	}

	roots := make([]string, 0)
	for drive := 'A'; drive <= 'Z'; drive++ {
		root := string(drive) + `:\`
		if _, err := os.Stat(root); err == nil {
			roots = append(roots, root)
		}
	}
	return roots
}

func formatUTCOffset(seconds int) string {
	sign := "+"
	if seconds < 0 {
		sign = "-"
		seconds = -seconds
	}
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	return fmt.Sprintf("UTC%s%02d:%02d", sign, hours, minutes)
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

type filesystemDirectoryPayload struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type filesystemDirectoryItem struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type providerConfig struct {
	Downloads *providerDownloadSettings `json:"downloads,omitempty"`
	Webhook   *providerWebhookSettings  `json:"webhook,omitempty"`
}

type providerWebhookSettings struct {
	PathPrefixes []string `json:"path_prefixes,omitempty"`
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
	Entry      *model.Entry
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
		if err := a.validateProviderRootPath(r.Context(), provider); err != nil {
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
	if len(parts) == 2 && parts[1] == "directories" {
		a.handleProviderDirectories(w, r, id)
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
		current, err := a.providers.Get(r.Context(), id)
		if err != nil {
			handleStorageError(w, err)
			return
		}
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
		providerChanged := current != nil && (current.Type != provider.Type || current.RootPath != provider.RootPath)
		if providerChanged {
			if err := a.providerCache.DeleteProvider(r.Context(), id); err != nil {
				handleStorageError(w, err)
				return
			}
		}
		if err := a.validateProviderRootPath(r.Context(), provider); err != nil {
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
		if err := a.providerCache.DeleteProvider(r.Context(), id); err != nil {
			handleStorageError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleProviderDirectories(w http.ResponseWriter, r *http.Request, id string) {
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

	cloudRoot := r.URL.Query().Get("cloud_root") == "true"
	if cloudRoot {
		providerModel.RootPath = "/"
	}
	runtimeProvider, ok, err := a.buildProvider(*providerModel)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotImplemented, "provider directories not implemented")
		return
	}

	providerPath := normalizeProviderPath(r.URL.Query().Get("path"))
	if !cloudRoot && providerPath == "/" && providerModel.Type != "local" {
		providerPath = normalizeProviderPath(providerModel.RootPath)
	}
	if r.URL.Query().Get("force") == "true" {
		if err := a.providerCache.Delete(r.Context(), id, "children:"+providerPath); err != nil {
			handleStorageError(w, err)
			return
		}
	}
	entries, err := runtimeProvider.List(r.Context(), providerPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	directories := make([]filesystemDirectoryItem, 0)
	for _, entry := range entries {
		if !entry.IsDir {
			continue
		}
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			name = path.Base(normalizeProviderPath(entry.Path))
		}
		directories = append(directories, filesystemDirectoryItem{Name: name, Path: normalizeProviderPath(entry.Path)})
	}
	sort.Slice(directories, func(i, j int) bool {
		return strings.ToLower(directories[i].Name) < strings.ToLower(directories[j].Name)
	})

	parentPath := ""
	if providerPath != "/" && !(providerModel.Type != "local" && normalizeProviderPath(providerPath) == normalizeProviderPath(providerModel.RootPath)) {
		parentPath = normalizeProviderPath(path.Dir(providerPath))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"provider_id": id,
		"path":        providerPath,
		"parent_path": parentPath,
		"items":       directories,
	})
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
		if err := a.providerCache.DeleteProvider(r.Context(), providerID); err != nil {
			handleStorageError(w, err)
			return
		}
		provider, err := a.providers.Get(r.Context(), providerID)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		if provider != nil {
			if err := a.validateProviderRootPath(r.Context(), *provider); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
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
		if err := a.providerCache.DeleteProvider(r.Context(), providerID); err != nil {
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
	ScanCron    string `json:"scan_cron,omitempty"`
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
	TargetPath string `json:"target_path,omitempty"`
	Overwrite  bool   `json:"overwrite,omitempty"`
}

type scanOptions struct {
	Overwrite bool
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
		cleanupOutputs := r.URL.Query().Get("cleanup_outputs") == "true"
		var cleanupMounts []model.LibraryMount
		if cleanupOutputs {
			mounts, err := a.libraries.ListMounts(r.Context(), id)
			if err != nil {
				handleStorageError(w, err)
				return
			}
			cleanupMounts = mounts
		}
		if err := a.libraries.Delete(r.Context(), id); err != nil {
			handleStorageError(w, err)
			return
		}
		if cleanupOutputs {
			if err := a.cleanupMountOutputDirs(cleanupMounts); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
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
		if err := a.validateMountSourcePath(r.Context(), item); err != nil {
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
		if err := a.validateMountSourcePath(r.Context(), item); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.libraries.UpdateMount(r.Context(), item); err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		cleanupOutputs := r.URL.Query().Get("cleanup_outputs") == "true"
		var cleanupMount model.LibraryMount
		if cleanupOutputs {
			mounts, err := a.libraries.ListMounts(r.Context(), libraryID)
			if err != nil {
				handleStorageError(w, err)
				return
			}
			mount, ok := findLibraryMountByID(mounts, mountID)
			if !ok {
				writeError(w, http.StatusNotFound, "resource not found")
				return
			}
			cleanupMount = mount
		}
		if err := a.libraries.DeleteMount(r.Context(), libraryID, mountID); err != nil {
			handleStorageError(w, err)
			return
		}
		if cleanupOutputs {
			if err := a.cleanupMountOutputDirs([]model.LibraryMount{cleanupMount}); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
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
		if key == systemTimezoneSettingKey {
			value, ok := payload.Value.(string)
			if !ok {
				writeError(w, http.StatusBadRequest, "timezone must be a string")
				return
			}
			if !validTimezoneName(value) {
				writeError(w, http.StatusBadRequest, "invalid timezone")
				return
			}
			payload.Value = strings.TrimSpace(value)
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
	limit := 50
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
	items, total, err := a.tasks.ListPage(r.Context(), limit, offset)
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

func (a *App) handleDashboardSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	providerCount, err := a.providers.Count(r.Context())
	if err != nil {
		handleStorageError(w, err)
		return
	}
	libraryCount, err := a.libraries.Count(r.Context())
	if err != nil {
		handleStorageError(w, err)
		return
	}
	taskCount, err := a.tasks.Count(r.Context())
	if err != nil {
		handleStorageError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"provider_count": providerCount,
		"library_count":  libraryCount,
		"task_count":     taskCount,
	})
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
	if len(parts) == 2 && parts[1] == "cancel" {
		a.handleCancelTask(w, r, parts[0])
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
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	eventType := strings.TrimSpace(r.URL.Query().Get("event_type"))
	beforeCreatedAt := strings.TrimSpace(r.URL.Query().Get("before_created_at"))
	beforeID := strings.TrimSpace(r.URL.Query().Get("before_id"))
	if (beforeCreatedAt == "") != (beforeID == "") {
		writeError(w, http.StatusBadRequest, "before_created_at and before_id must be provided together")
		return
	}

	items, hasMore, err := a.events.List(r.Context(), storage.SystemEventListOptions{
		Limit:           limit,
		Source:          source,
		EventType:       eventType,
		BeforeCreatedAt: beforeCreatedAt,
		BeforeID:        beforeID,
	})
	if err != nil {
		handleStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "has_more": hasMore})
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

func (a *App) handleCancelTask(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	item, err := a.cancelScanTask(r.Context(), taskID)
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
	var payload scanLibraryPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	activeLibrary, err := a.tasks.FindActiveByType(r.Context(), "library_scan")
	if err != nil {
		handleStorageError(w, err)
		return
	}
	if activeLibrary != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "library scan already running", "task": activeLibrary})
		return
	}
	task, err := a.createScanTask(r.Context(), model.ScanTask{TaskType: "full_scan", Status: model.TaskStatusPending, Message: "queued full scan task"})
	if err != nil {
		handleStorageError(w, err)
		return
	}
	go a.runFullScan(task.ID, scanOptions{Overwrite: payload.Overwrite})
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
	activeFull, err := a.tasks.FindActive(r.Context(), "full_scan", "")
	if err != nil {
		handleStorageError(w, err)
		return
	}
	if activeFull != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "full scan already running", "task": activeFull})
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
	activeLibraryCount, err := a.tasks.CountActiveByType(r.Context(), "library_scan")
	if err != nil {
		handleStorageError(w, err)
		return
	}
	if activeLibraryCount >= maxConcurrentLibraryScans {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "too many library scans already running", "limit": maxConcurrentLibraryScans})
		return
	}
	message := "queued library scan task"
	if strings.TrimSpace(payload.SourcePath) != "" || strings.TrimSpace(payload.TargetPath) != "" {
		message = "queued partial library scan task"
	}
	task, err := a.createScanTask(r.Context(), model.ScanTask{TaskType: "library_scan", LibraryID: libraryID, Status: model.TaskStatusPending, Message: message})
	if err != nil {
		handleStorageError(w, err)
		return
	}
	go a.runLibraryScanTask(task.ID, libraryID, payload.SourcePath, payload.TargetPath, scanOptions{Overwrite: payload.Overwrite})
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

func (a *App) registerActiveTask(taskID string, cancel context.CancelFunc) {
	a.activeTaskMu.Lock()
	defer a.activeTaskMu.Unlock()
	a.activeTasks[taskID] = cancel
}

func (a *App) unregisterActiveTask(taskID string) {
	a.activeTaskMu.Lock()
	defer a.activeTaskMu.Unlock()
	delete(a.activeTasks, taskID)
}

func (a *App) cancelActiveTask(taskID string) bool {
	a.activeTaskMu.Lock()
	cancel := a.activeTasks[taskID]
	a.activeTaskMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (a *App) cancelScanTask(ctx context.Context, taskID string) (*model.ScanTask, error) {
	task, err := a.tasks.Get(ctx, taskID)
	if err != nil || task == nil {
		return task, err
	}
	if task.Status != model.TaskStatusPending && task.Status != model.TaskStatusRunning {
		return task, nil
	}
	a.cancelActiveTask(taskID)
	task.Status = model.TaskStatusCancelled
	task.Message = "scan cancelled"
	task.ErrorMessage = ""
	task.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	if err := a.tasks.Update(ctx, *task); err != nil {
		return nil, err
	}
	a.appendTaskLog(ctx, taskID, "warning", "scan cancelled", nil)
	return a.tasks.Get(ctx, taskID)
}

func (a *App) markTaskCancelled(taskID string) {
	ctx := context.Background()
	task, err := a.tasks.Get(ctx, taskID)
	if err != nil || task == nil || task.Status == model.TaskStatusCancelled {
		return
	}
	task.Status = model.TaskStatusCancelled
	task.Message = "scan cancelled"
	task.ErrorMessage = ""
	task.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	a.appendTaskLog(ctx, taskID, "warning", "scan cancelled", nil)
	_ = a.tasks.Update(ctx, *task)
}

func (a *App) runFullScan(taskID string, options scanOptions) {
	ctx, cancel := context.WithCancel(context.Background())
	a.registerActiveTask(taskID, cancel)
	defer a.unregisterActiveTask(taskID)
	defer cancel()
	a.appendTaskLog(ctx, taskID, "info", "starting full scan", map[string]any{"overwrite": options.Overwrite})
	libraries, err := a.libraries.ListEnabled(ctx)
	if err != nil {
		a.failTask(ctx, taskID, err)
		return
	}

	task, err := a.tasks.Get(ctx, taskID)
	if err != nil || task == nil {
		return
	}
	if task.Status == model.TaskStatusCancelled {
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
		if err := ctx.Err(); err != nil {
			a.markTaskCancelled(taskID)
			return
		}
		a.appendTaskLog(ctx, taskID, "info", "scanning library", map[string]any{"library_id": library.ID})
		if err := a.scanLibrary(ctx, taskID, library.ID, "", "", options); err != nil {
			if errors.Is(err, context.Canceled) {
				a.markTaskCancelled(taskID)
				return
			}
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

func (a *App) runLibraryScanTask(taskID, libraryID, sourcePath, targetPath string, options scanOptions) {
	ctx, cancel := context.WithCancel(context.Background())
	a.registerActiveTask(taskID, cancel)
	defer a.unregisterActiveTask(taskID)
	defer cancel()
	normalizedSourcePath := ""
	if strings.TrimSpace(sourcePath) != "" {
		normalizedSourcePath = normalizeProviderPath(sourcePath)
	}
	normalizedTargetPath := ""
	if strings.TrimSpace(targetPath) != "" {
		normalizedTargetPath = normalizeProviderPath(targetPath)
	}
	a.appendTaskLog(ctx, taskID, "info", "starting library scan", map[string]any{"library_id": libraryID, "source_path": normalizedSourcePath, "target_path": normalizedTargetPath, "overwrite": options.Overwrite})
	task, err := a.tasks.Get(ctx, taskID)
	if err != nil || task == nil {
		return
	}
	if task.Status == model.TaskStatusCancelled {
		return
	}
	task.Status = model.TaskStatusRunning
	task.ProgressTotal = 1
	task.ProgressDone = 0
	if normalizedSourcePath == "" && normalizedTargetPath == "" {
		task.Message = "running library scan"
	} else {
		task.Message = "running partial library scan"
	}
	if err := a.tasks.Update(ctx, *task); err != nil {
		return
	}

	if err := a.scanLibrary(ctx, taskID, libraryID, normalizedSourcePath, normalizedTargetPath, options); err != nil {
		if errors.Is(err, context.Canceled) {
			a.markTaskCancelled(taskID)
			return
		}
		a.failTask(ctx, taskID, err)
		return
	}

	task.Status = model.TaskStatusCompleted
	task.ProgressDone = 1
	task.Message = "library scan completed"
	task.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	a.appendTaskLog(ctx, taskID, "info", "library scan completed", map[string]any{"library_id": libraryID, "source_path": normalizedSourcePath, "target_path": normalizedTargetPath})
	_ = a.tasks.Update(ctx, *task)
}

func (a *App) runLibraryCurrentLevelScanTask(taskID, libraryID, sourcePath string, options scanOptions) {
	ctx, cancel := context.WithCancel(context.Background())
	a.registerActiveTask(taskID, cancel)
	defer a.unregisterActiveTask(taskID)
	defer cancel()
	normalizedSourcePath := normalizeProviderPath(sourcePath)
	a.appendTaskLog(ctx, taskID, "info", "starting current-level library scan", map[string]any{"library_id": libraryID, "source_path": normalizedSourcePath, "overwrite": options.Overwrite})
	task, err := a.tasks.Get(ctx, taskID)
	if err != nil || task == nil {
		return
	}
	if task.Status == model.TaskStatusCancelled {
		return
	}
	task.Status = model.TaskStatusRunning
	task.ProgressTotal = 1
	task.ProgressDone = 0
	task.Message = "running current-level library scan"
	if err := a.tasks.Update(ctx, *task); err != nil {
		return
	}

	if err := a.scanLibraryCurrentLevel(ctx, taskID, libraryID, normalizedSourcePath, options); err != nil {
		if errors.Is(err, context.Canceled) {
			a.markTaskCancelled(taskID)
			return
		}
		a.failTask(ctx, taskID, err)
		return
	}

	task.Status = model.TaskStatusCompleted
	task.ProgressDone = 1
	task.Message = "library scan completed"
	task.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	a.appendTaskLog(ctx, taskID, "info", "current-level library scan completed", map[string]any{"library_id": libraryID, "source_path": normalizedSourcePath})
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

func (a *App) scanLibraryCurrentLevel(ctx context.Context, taskID, libraryID, sourcePath string, options scanOptions) error {
	mounts, err := a.libraries.ListEnabledMounts(ctx, libraryID)
	if err != nil {
		return err
	}
	sourcePath = normalizeProviderPath(sourcePath)
	mount, ok := findMountForSourcePath(mounts, sourcePath)
	if !ok {
		return fmt.Errorf("source path %s is not under an enabled mount for library %s", sourcePath, libraryID)
	}
	providerModel, err := a.providers.Get(ctx, mount.ProviderID)
	if err != nil {
		return err
	}
	if providerModel == nil {
		return fmt.Errorf("provider %s not found", mount.ProviderID)
	}
	runtimeProvider, ok, err := a.buildProvider(*providerModel)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("provider type %s not implemented yet", providerModel.Type)
	}
	if !providerPathWithinRoot(sourcePath, mount.SourcePath) {
		return fmt.Errorf("scan source path %s is outside mount source path %s", sourcePath, mount.SourcePath)
	}

	downloads := providerDownloadOptionsFor(*providerModel)
	seenAt := time.Now().UTC().Format(time.RFC3339)
	targetRoot := a.mountTargetDirForProviderDir(mount, sourcePath)
	entries, err := runtimeProvider.List(ctx, sourcePath)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if taskID != "" {
		a.appendTaskLog(ctx, taskID, "info", "scanning current directory", map[string]any{"mount_id": mount.ID, "provider_id": mount.ProviderID, "source_path": mount.SourcePath, "scan_source_path": sourcePath, "target_path": mount.TargetPath, "downloads": downloads, "overwrite": options.Overwrite})
	}

	filesByDir := map[string][]provideriface.Entry{sourcePath: {}}
	mediaEntries := make([]provideriface.Entry, 0)
	fileCount := 0
	dirCount := 0
	if entriesContainIgnoreFile(entries) {
		if err := a.cleanupIgnoredOutputDir(mount, sourcePath); err != nil {
			return err
		}
		if err := a.entries.DeleteUnderPrefix(ctx, providerModel.ID, sourcePath); err != nil {
			return err
		}
		if taskID != "" {
			a.appendTaskLog(ctx, taskID, "info", "current directory ignored", map[string]any{"scan_source_path": sourcePath, "target_root": targetRoot, "deleted_output_dir": true})
		}
		return a.libraries.MarkScanned(ctx, libraryID, time.Now().UTC().Format(time.RFC3339))
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		entryType := "file"
		if entry.IsDir {
			entryType = "dir"
			dirCount++
		} else {
			fileCount++
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
		if !entry.IsDir {
			filesByDir[sourcePath] = append(filesByDir[sourcePath], entry)
		}
		if !entry.IsDir && isMediaFile(entry.Name) {
			mediaEntries = append(mediaEntries, entry)
		}
	}
	if taskID != "" {
		a.appendTaskLog(ctx, taskID, "info", "current directory enumerated", map[string]any{"scan_source_path": sourcePath, "entries": len(entries), "dirs": dirCount, "files": fileCount, "media_files": len(mediaEntries)})
	}

	expectedSTRM := make(map[string]struct{})
	syncedOutputs := make(map[string]struct{})
	syncJob := func(job outputSyncJob) error {
		if _, exists := syncedOutputs[job.TargetPath]; exists {
			return nil
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
		if !options.Overwrite && fileExists(job.TargetPath) {
			syncedOutputs[job.TargetPath] = struct{}{}
			if taskID != "" {
				a.appendTaskLog(ctx, taskID, "info", fmt.Sprintf("skip existing %s", job.Kind), map[string]any{"provider_path": job.SourcePath, "output_path": job.TargetPath})
			}
			return nil
		}
		if err := a.downloadProviderFile(ctx, runtimeProvider, job.SourcePath, job.TargetPath, job.Entry, progress); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			if taskID != "" {
				a.appendTaskLog(ctx, taskID, "warning", fmt.Sprintf("skip failed %s sync", job.Kind), map[string]any{"provider_path": job.SourcePath, "output_path": job.TargetPath, "error": err.Error()})
			}
			return nil
		}
		syncedOutputs[job.TargetPath] = struct{}{}
		if taskID != "" {
			a.appendTaskLog(ctx, taskID, "info", fmt.Sprintf("downloaded %s", job.Kind), map[string]any{"provider_path": job.SourcePath, "output_path": job.TargetPath})
		}
		return nil
	}

	for dirPath, dirEntries := range filesByDir {
		for _, job := range a.buildDirectoryOutputSyncJobs(mount, dirPath, dirEntries, downloads) {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := syncJob(job); err != nil {
				return err
			}
		}
	}

	for _, mediaEntry := range mediaEntries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if downloads.STRM {
			outPath, written, err := a.writeSTRM(providerModel.ID, mount, mediaEntry.Path, options.Overwrite)
			if err != nil {
				return err
			}
			expectedSTRM[outPath] = struct{}{}
			if taskID != "" && !written {
				a.appendTaskLog(ctx, taskID, "info", "skip existing strm", map[string]any{"provider_path": mediaEntry.Path, "output_path": outPath})
			}
		}
		jobs := a.buildOutputSyncJobs(mount, mediaEntry, filesByDir[sourcePath], downloads)
		for _, job := range jobs {
			if err := syncJob(job); err != nil {
				return err
			}
		}
	}

	deletedCount, err := cleanupStaleSTRMCurrentDir(targetRoot, expectedSTRM)
	if err != nil {
		return err
	}
	if taskID != "" {
		a.appendTaskLog(ctx, taskID, "info", "current-level cleanup completed", map[string]any{"target_root": targetRoot, "deleted": deletedCount})
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return a.libraries.MarkScanned(ctx, libraryID, now)
}

func (a *App) scanLibrary(ctx context.Context, taskID, libraryID, sourcePath, targetPath string, options scanOptions) error {
	mounts, err := a.libraries.ListEnabledMounts(ctx, libraryID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(sourcePath) != "" && strings.TrimSpace(targetPath) != "" {
		return fmt.Errorf("source_path and target_path cannot both be provided")
	}
	if strings.TrimSpace(targetPath) != "" {
		var ok bool
		targetPath = normalizeProviderPath(targetPath)
		sourcePath, ok = sourcePathForTargetPath(mounts, targetPath)
		if !ok {
			return fmt.Errorf("target path %s is not under an enabled mount for library %s", targetPath, libraryID)
		}
	}
	if strings.TrimSpace(sourcePath) != "" {
		sourcePath = normalizeProviderPath(sourcePath)
		mount, ok := findMountForSourcePath(mounts, sourcePath)
		if !ok {
			return fmt.Errorf("source path %s is not under an enabled mount for library %s", sourcePath, libraryID)
		}
		targetRoot, expectedSTRM, err := a.scanMount(ctx, taskID, mount, sourcePath, options)
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
		targetRoot, expectedSTRM, err := a.scanMount(ctx, taskID, mount, mount.SourcePath, options)
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

func (a *App) scanMount(ctx context.Context, taskID string, mount model.LibraryMount, scanSourcePath string, options scanOptions) (string, map[string]struct{}, error) {
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
	entryCount := 0
	fileCount := 0
	dirCount := 0
	mediaCount := 0
	if taskID != "" {
		a.appendTaskLog(ctx, taskID, "info", "scanning mount", map[string]any{"mount_id": mount.ID, "provider_id": mount.ProviderID, "source_path": mount.SourcePath, "scan_source_path": scanSourcePath, "target_path": mount.TargetPath, "downloads": downloads, "overwrite": options.Overwrite})
	}

	ignoredDirs := 0
	walkOptions := provideriface.WalkOptions{OnIgnoredDir: func(ignoredPath string) error {
		ignoredDirs++
		if taskID != "" {
			a.appendTaskLog(ctx, taskID, "info", "ignore directory", map[string]any{"mount_id": mount.ID, "provider_id": mount.ProviderID, "path": ignoredPath})
		}
		return a.cleanupIgnoredOutputDir(mount, ignoredPath)
	}}
	if err := provider.WalkFiles(ctx, scanSourcePath, walkOptions, func(entry provideriface.Entry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entryCount++
		entryType := "file"
		if entry.IsDir {
			entryType = "dir"
			dirCount++
		} else {
			fileCount++
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
		if !entry.IsDir && isMediaFile(entry.Name) {
			mediaCount++
		}
		if taskID != "" && entryCount%scanProgressLogInterval == 0 {
			a.appendTaskLog(ctx, taskID, "info", "enumerating provider entries", map[string]any{"mount_id": mount.ID, "scan_source_path": scanSourcePath, "entries": entryCount, "dirs": dirCount, "files": fileCount, "media_files": mediaCount})
		}
		return nil
	}); err != nil {
		return "", nil, err
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if taskID != "" {
		a.appendTaskLog(ctx, taskID, "info", "provider entries enumerated", map[string]any{"mount_id": mount.ID, "scan_source_path": scanSourcePath, "entries": entryCount, "dirs": dirCount, "files": fileCount, "media_files": mediaCount, "ignored_dirs": ignoredDirs})
	}
	if err := a.entries.DeleteStaleUnderPrefix(ctx, providerModel.ID, scanSourcePath, seenAt); err != nil {
		return "", nil, err
	}

	indexedEntries, err := a.entries.ListUnderPrefix(ctx, providerModel.ID, scanSourcePath)
	if err != nil {
		return "", nil, err
	}
	filesByDir := make(map[string][]model.Entry)
	mediaEntries := make([]model.Entry, 0)
	for _, entry := range indexedEntries {
		if entry.EntryType == "dir" {
			continue
		}
		filesByDir[entry.ParentPath] = append(filesByDir[entry.ParentPath], entry)
		if isMediaFile(entry.Name) {
			mediaEntries = append(mediaEntries, entry)
		}
	}
	if taskID != "" {
		a.appendTaskLog(ctx, taskID, "info", "loaded indexed entries for generation", map[string]any{"mount_id": mount.ID, "scan_source_path": scanSourcePath, "indexed_entries": len(indexedEntries), "media_files": len(mediaEntries)})
	}

	syncedOutputs := make(map[string]struct{})
	syncJob := func(job outputSyncJob) error {
		if _, exists := syncedOutputs[job.TargetPath]; exists {
			return nil
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
		if !options.Overwrite && fileExists(job.TargetPath) {
			syncedOutputs[job.TargetPath] = struct{}{}
			if taskID != "" {
				a.appendTaskLog(ctx, taskID, "info", fmt.Sprintf("skip existing %s", job.Kind), map[string]any{"provider_path": job.SourcePath, "output_path": job.TargetPath})
			}
			return nil
		}
		if err := a.downloadProviderFile(ctx, runtimeProvider, job.SourcePath, job.TargetPath, job.Entry, progress); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			if taskID != "" {
				a.appendTaskLog(ctx, taskID, "warning", fmt.Sprintf("skip failed %s sync", job.Kind), map[string]any{"provider_path": job.SourcePath, "output_path": job.TargetPath, "error": err.Error()})
			}
			return nil
		}
		syncedOutputs[job.TargetPath] = struct{}{}
		if taskID != "" {
			a.appendTaskLog(ctx, taskID, "info", fmt.Sprintf("downloaded %s", job.Kind), map[string]any{"provider_path": job.SourcePath, "output_path": job.TargetPath})
		}
		return nil
	}

	for dirPath, dirEntries := range filesByDir {
		for _, job := range a.buildDirectoryOutputSyncJobsFromEntries(mount, dirPath, dirEntries, downloads) {
			if err := ctx.Err(); err != nil {
				return "", nil, err
			}
			if err := syncJob(job); err != nil {
				return "", nil, err
			}
		}
	}

	for _, mediaEntry := range mediaEntries {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		if downloads.STRM {
			outPath, written, err := a.writeSTRM(providerModel.ID, mount, mediaEntry.Path, options.Overwrite)
			if err != nil {
				return "", nil, err
			}
			expectedSTRM[outPath] = struct{}{}
			if taskID != "" && written {
				a.appendTaskLog(ctx, taskID, "info", "generated strm", map[string]any{"provider_path": mediaEntry.Path, "output_path": outPath})
			} else if taskID != "" {
				a.appendTaskLog(ctx, taskID, "info", "skip existing strm", map[string]any{"provider_path": mediaEntry.Path, "output_path": outPath})
			}
		}

		dirPath := path.Dir(mediaEntry.Path)
		if dirPath == "." {
			dirPath = "/"
		}
		jobs := a.buildOutputSyncJobsFromEntries(mount, mediaEntry, filesByDir[dirPath], downloads)
		for _, job := range jobs {
			if err := syncJob(job); err != nil {
				return "", nil, err
			}
		}
	}

	if taskID != "" {
		a.appendTaskLog(ctx, taskID, "info", "mount scan completed", map[string]any{"mount_id": mount.ID, "generated_strm": len(expectedSTRM), "downloaded_files": len(syncedOutputs), "target_root": targetRoot})
	}

	return targetRoot, expectedSTRM, nil
}

func entriesContainIgnoreFile(entries []provideriface.Entry) bool {
	for _, entry := range entries {
		if !entry.IsDir && entry.Name == provideriface.IgnoreFileName {
			return true
		}
	}
	return false
}

func (a *App) cleanupIgnoredOutputDir(mount model.LibraryMount, providerDir string) error {
	outputDir := a.mountTargetDirForProviderDir(mount, providerDir)
	if err := ensureOutputPathWithinRoot(a.config.Storage.STRMOutputDir, outputDir); err != nil {
		return err
	}
	if err := os.RemoveAll(outputDir); err != nil {
		return fmt.Errorf("remove ignored output dir %s: %w", outputDir, err)
	}
	return nil
}

func (a *App) writeSTRM(providerID string, mount model.LibraryMount, providerPath string, overwrite bool) (string, bool, error) {
	paths := a.mediaOutputPaths(mount, providerPath)
	outPath := filepath.Join(paths.TargetDir, paths.BaseName+".strm")
	if !overwrite && fileExists(outPath) {
		return filepath.Clean(outPath), false, nil
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", false, fmt.Errorf("create strm dir: %w", err)
	}

	streamURL := strings.TrimRight(a.config.Server.PublicBaseURL, "/") + "/stream/" + providerID + escapeProviderPath(providerPath)
	if err := os.WriteFile(outPath, []byte(streamURL), 0o644); err != nil {
		return "", false, fmt.Errorf("write strm file %s: %w", outPath, err)
	}
	return filepath.Clean(outPath), true, nil
}

func fileExists(filePath string) bool {
	info, err := os.Stat(filePath)
	return err == nil && !info.IsDir()
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

func entryMetadataMap(metadataJSON string) map[string]string {
	metadata := make(map[string]string)
	if strings.TrimSpace(metadataJSON) == "" {
		return metadata
	}
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return make(map[string]string)
	}
	return metadata
}

func (a *App) downloadProviderFile(ctx context.Context, runtimeProvider provideriface.Provider, providerPath, targetPath string, entry *model.Entry, progress downloadProgressFunc) error {
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

	var lastErr error
	for attempt := 1; attempt <= providerDownloadAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			_ = os.Remove(tmpPath)
			return err
		}
		if progress != nil {
			progress("request direct link", map[string]any{"attempt": attempt, "max_attempts": providerDownloadAttempts})
		}
		directLink, err := getDirectLinkForDownload(ctx, runtimeProvider, providerPath, entry)
		if err != nil {
			lastErr = err
		} else if directLink == nil || directLink.URL == "" {
			lastErr = fmt.Errorf("provider returned empty direct link")
		} else {
			if progress != nil {
				progress("direct link ready", map[string]any{"attempt": attempt, "max_attempts": providerDownloadAttempts, "direct_link_url": directLink.URL})
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, directLink.URL, nil)
			if err != nil {
				return fmt.Errorf("build download request: %w", err)
			}
			for key, value := range directLink.Headers {
				req.Header.Set(key, value)
			}
			if progress != nil {
				progress("download started", map[string]any{"attempt": attempt, "max_attempts": providerDownloadAttempts, "request_url": directLink.URL})
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				lastErr = fmt.Errorf("download file: %w", err)
			} else {
				if resp.StatusCode < 200 || resp.StatusCode >= 300 {
					lastErr = fmt.Errorf("download file: unexpected status %s", resp.Status)
					_ = resp.Body.Close()
				} else {
					target, err := os.Create(tmpPath)
					if err != nil {
						_ = resp.Body.Close()
						return fmt.Errorf("create target file %s: %w", tmpPath, err)
					}
					_, copyErr := io.Copy(target, resp.Body)
					bodyCloseErr := resp.Body.Close()
					closeErr := target.Close()
					if copyErr != nil {
						_ = os.Remove(tmpPath)
						lastErr = fmt.Errorf("write target file %s: %w", tmpPath, copyErr)
					} else if bodyCloseErr != nil {
						_ = os.Remove(tmpPath)
						lastErr = fmt.Errorf("close download body: %w", bodyCloseErr)
					} else if closeErr != nil {
						_ = os.Remove(tmpPath)
						return fmt.Errorf("close target file %s: %w", tmpPath, closeErr)
					} else if err := os.Rename(tmpPath, targetPath); err != nil {
						_ = os.Remove(tmpPath)
						return fmt.Errorf("replace target file %s: %w", targetPath, err)
					} else {
						if progress != nil {
							progress("download finished", map[string]any{"attempt": attempt, "status": resp.Status, "content_length": resp.ContentLength})
						}
						return nil
					}
				}
			}
		}
		if progress != nil {
			progress("download attempt failed", map[string]any{"attempt": attempt, "max_attempts": providerDownloadAttempts, "error": lastErr.Error()})
		}
	}
	return lastErr
}

func getDirectLinkForDownload(ctx context.Context, runtimeProvider provideriface.Provider, providerPath string, entry *model.Entry) (*provideriface.DirectLinkResult, error) {
	input := provideriface.DirectLinkInput{Path: providerPath}
	if entry != nil {
		input.Path = entry.Path
		input.ProviderEntryID = entry.ProviderEntryID
		input.Metadata = entryMetadataMap(entry.MetadataJSON)
	}
	return runtimeProvider.GetDirectLinkForEntry(ctx, input)
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
	entry, err := a.entries.Get(ctx, providerID, providerPath)
	if err != nil {
		log.Printf("load stream entry provider=%s path=%s: %v", providerID, providerPath, err)
	}
	directLink, err := getDirectLinkForDownload(ctx, runtimeProvider, providerPath, entry)
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
		provider, err := cookie115provider.New(providerModel.ID, providerModel.RootPath, cookieValue, secrets["user_agent"], providerCacheScope{app: a, providerID: providerModel.ID})
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
		a.recordSystemEvent(context.Background(), "provider_auth_error", "error", "provider", "failed to persist provider credential", map[string]any{"provider_id": providerID, "secret_type": secretType, "error": err.Error()})
	}
}

func (a *App) recordProviderAuthError(ctx context.Context, providerModel model.Provider, authType, stage string, err error) {
	if err == nil {
		return
	}
	a.recordSystemEvent(ctx, "provider_auth_error", "error", "provider", "provider authorization failed", map[string]any{
		"provider_id":   providerModel.ID,
		"provider_type": providerModel.Type,
		"provider_name": providerModel.Name,
		"auth_type":     authType,
		"stage":         stage,
		"error":         err.Error(),
	})
}

func toLibraryModel(payload libraryPayload) (model.Library, error) {
	if payload.ID == "" {
		return model.Library{}, fmt.Errorf("id is required")
	}
	if payload.Name == "" {
		return model.Library{}, fmt.Errorf("name is required")
	}
	scanCron := strings.TrimSpace(payload.ScanCron)
	if scanCron != "" {
		if _, err := parseCronSchedule(scanCron); err != nil {
			return model.Library{}, fmt.Errorf("invalid scan_cron: %w", err)
		}
	}
	return model.Library{
		ID:          payload.ID,
		Name:        payload.Name,
		Description: payload.Description,
		Enabled:     payload.Enabled,
		LastScanAt:  payload.LastScanAt,
		ScanCron:    scanCron,
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

func (a *App) validateProviderRootPath(ctx context.Context, providerModel model.Provider) error {
	if providerModel.Type == "local" {
		info, err := os.Stat(providerModel.RootPath)
		if err != nil {
			return fmt.Errorf("root_path %s not found: %w", providerModel.RootPath, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("root_path %s is not a directory", providerModel.RootPath)
		}
		return nil
	}
	if !a.providerHasRequiredSecrets(ctx, providerModel) {
		return nil
	}
	runtimeProvider, ok, err := a.buildProvider(providerModel)
	if err != nil {
		if isProviderCredentialError(err) {
			return nil
		}
		return err
	}
	if !ok {
		return nil
	}
	entry, err := runtimeProvider.Stat(ctx, providerModel.RootPath)
	if err != nil {
		return fmt.Errorf("root_path %s not found: %w", providerModel.RootPath, err)
	}
	if entry == nil || !entry.IsDir {
		return fmt.Errorf("root_path %s is not a directory", providerModel.RootPath)
	}
	return nil
}

func (a *App) providerHasRequiredSecrets(ctx context.Context, providerModel model.Provider) bool {
	secrets, err := a.loadProviderSecretValues(ctx, providerModel.ID)
	if err != nil {
		return false
	}
	switch providerModel.Type {
	case "115cookie":
		return strings.TrimSpace(secrets["cookie"]) != ""
	case "115open":
		return strings.TrimSpace(secrets["access_token"]) != "" || strings.TrimSpace(secrets["refresh_token"]) != ""
	default:
		return true
	}
}

func (a *App) validateMountSourcePath(ctx context.Context, mount model.LibraryMount) error {
	providerModel, err := a.providers.Get(ctx, mount.ProviderID)
	if err != nil {
		return err
	}
	if providerModel == nil {
		return fmt.Errorf("provider %s not found", mount.ProviderID)
	}
	sourcePath := normalizeProviderPath(mount.SourcePath)
	rootPath := normalizeProviderPath(providerModel.RootPath)
	if providerModel.Type != "local" && !providerPathWithinRoot(sourcePath, rootPath) {
		return fmt.Errorf("source_path %s is outside provider root_path %s", sourcePath, rootPath)
	}
	runtimeProvider, ok, err := a.buildProvider(*providerModel)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	entry, err := runtimeProvider.Stat(ctx, mount.SourcePath)
	if err != nil {
		return fmt.Errorf("source_path %s not found: %w", mount.SourcePath, err)
	}
	if entry == nil || !entry.IsDir {
		return fmt.Errorf("source_path %s is not a directory", mount.SourcePath)
	}
	return nil
}

func isProviderCredentialError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "provider secret") || strings.Contains(message, "credential") || strings.Contains(message, "token") || strings.Contains(message, "cookie")
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

func (a *App) buildOutputSyncJobsFromEntries(mount model.LibraryMount, mediaEntry model.Entry, dirEntries []model.Entry, downloads providerDownloadOptions) []outputSyncJob {
	paths := a.mediaOutputPaths(mount, mediaEntry.Path)
	baseNameLower := strings.ToLower(paths.BaseName)
	jobs := make([]outputSyncJob, 0)
	for i := range dirEntries {
		entry := dirEntries[i]
		if entry.Path == mediaEntry.Path {
			continue
		}
		job, ok := classifyOutputSyncJob(entryProviderEntry(entry), paths, baseNameLower, downloads)
		if !ok {
			continue
		}
		job.Entry = &dirEntries[i]
		jobs = append(jobs, job)
	}
	return jobs
}

func (a *App) buildDirectoryOutputSyncJobs(mount model.LibraryMount, providerDir string, dirEntries []provideriface.Entry, downloads providerDownloadOptions) []outputSyncJob {
	targetDir := a.mountTargetDirForProviderDir(mount, providerDir)
	jobs := make([]outputSyncJob, 0)
	for _, entry := range dirEntries {
		job, ok := classifyDirectoryOutputSyncJob(entry, targetDir, downloads)
		if !ok {
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs
}

func (a *App) buildDirectoryOutputSyncJobsFromEntries(mount model.LibraryMount, providerDir string, dirEntries []model.Entry, downloads providerDownloadOptions) []outputSyncJob {
	targetDir := a.mountTargetDirForProviderDir(mount, providerDir)
	jobs := make([]outputSyncJob, 0)
	for i := range dirEntries {
		entry := dirEntries[i]
		job, ok := classifyDirectoryOutputSyncJob(entryProviderEntry(entry), targetDir, downloads)
		if !ok {
			continue
		}
		job.Entry = &dirEntries[i]
		jobs = append(jobs, job)
	}
	return jobs
}

func entryProviderEntry(entry model.Entry) provideriface.Entry {
	return provideriface.Entry{
		ID:       entry.ProviderEntryID,
		Name:     entry.Name,
		Path:     entry.Path,
		IsDir:    entry.EntryType == "dir",
		Size:     entry.Size,
		ModTime:  entry.MTime,
		MimeType: entry.MimeType,
		Metadata: entryMetadataMap(entry.MetadataJSON),
	}
}

func classifyDirectoryOutputSyncJob(entry provideriface.Entry, targetDir string, downloads providerDownloadOptions) (outputSyncJob, bool) {
	nameLower := strings.ToLower(entry.Name)
	if downloads.NFO && (nameLower == "tvshow.nfo" || nameLower == "season.nfo") {
		return outputSyncJob{Kind: "nfo", SourcePath: entry.Path, TargetPath: filepath.Join(targetDir, entry.Name)}, true
	}
	if downloads.Images && isDirectoryArtwork(nameLower) {
		return outputSyncJob{Kind: "image", SourcePath: entry.Path, TargetPath: filepath.Join(targetDir, entry.Name)}, true
	}
	return outputSyncJob{}, false
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
	return stem == baseNameLower || strings.HasPrefix(stem, baseNameLower+".") || strings.HasPrefix(stem, baseNameLower+"-")
}

func isBIFSidecar(baseNameLower, nameLower string) bool {
	if strings.ToLower(filepath.Ext(nameLower)) != ".bif" {
		return false
	}
	stem := strings.TrimSuffix(nameLower, ".bif")
	return stem == baseNameLower || strings.HasPrefix(stem, baseNameLower+".") || strings.HasPrefix(stem, baseNameLower+"-") || stem == "index"
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

func isDirectoryArtwork(nameLower string) bool {
	ext := strings.ToLower(filepath.Ext(nameLower))
	if _, ok := imageExtensions[ext]; !ok {
		return false
	}
	stem := strings.TrimSuffix(nameLower, ext)
	if _, ok := artworkBaseNames[stem]; ok {
		return true
	}
	return strings.HasPrefix(stem, "season")
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

func (a *App) cleanupMountOutputDirs(mounts []model.LibraryMount) error {
	outputRoot := filepath.Clean(a.config.Storage.STRMOutputDir)
	targets := make(map[string]struct{})
	for _, mount := range mounts {
		targetDir := filepath.Clean(filepath.Join(outputRoot, filepath.FromSlash(strings.TrimPrefix(normalizeProviderPath(mount.TargetPath), "/"))))
		if !pathWithinRoot(targetDir, outputRoot) {
			return fmt.Errorf("refuse to clean output path outside strm output dir: %s", targetDir)
		}
		targets[targetDir] = struct{}{}
	}

	for targetDir := range targets {
		if err := os.RemoveAll(targetDir); err != nil {
			return fmt.Errorf("remove output dir %s: %w", targetDir, err)
		}
	}
	return nil
}

func (a *App) cleanupWebhookDeletedTargets(ctx context.Context, payload filesystemWebhookPayload) (int, error) {
	libraries, err := a.libraries.ListEnabled(ctx)
	if err != nil {
		return 0, err
	}
	deleted := 0
	seen := make(map[string]struct{})
	for _, library := range libraries {
		if payload.LibraryID != "" && library.ID != payload.LibraryID {
			continue
		}
		mounts, err := a.libraries.ListEnabledMounts(ctx, library.ID)
		if err != nil {
			return deleted, err
		}
		for _, mount := range mounts {
			if payload.ProviderID != "" && mount.ProviderID != payload.ProviderID {
				continue
			}
			webhookPaths, err := a.webhookPayloadPathsForProvider(ctx, mount.ProviderID, payload)
			if err != nil {
				return deleted, err
			}
			for _, webhookPath := range webhookPaths {
				if !providerPathWithinRoot(webhookPath, mount.SourcePath) {
					continue
				}
				key := mount.ProviderID + "\x00" + webhookPath
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				count, err := a.cleanupWebhookDeletedPath(ctx, mount, webhookPath, payload.IsDir != nil && *payload.IsDir)
				if err != nil {
					return deleted, err
				}
				deleted += count
			}
		}
	}
	return deleted, nil
}

func (a *App) cleanupWebhookDeletedPath(ctx context.Context, mount model.LibraryMount, providerPath string, isDir bool) (int, error) {
	providerPath = normalizeProviderPath(providerPath)
	if isDir {
		outputDir := a.mountTargetDirForProviderDir(mount, providerPath)
		if err := ensureOutputPathWithinRoot(a.config.Storage.STRMOutputDir, outputDir); err != nil {
			return 0, err
		}
		if err := os.RemoveAll(outputDir); err != nil {
			return 0, fmt.Errorf("remove output dir %s: %w", outputDir, err)
		}
		if err := a.entries.DeleteUnderPrefix(ctx, mount.ProviderID, providerPath); err != nil {
			return 0, err
		}
		return 1, nil
	}

	deleted, err := a.removeWebhookFileOutputs(mount, providerPath)
	if err != nil {
		return deleted, err
	}
	if err := a.entries.DeletePath(ctx, mount.ProviderID, providerPath); err != nil {
		return deleted, err
	}
	return deleted, nil
}

func (a *App) removeWebhookFileOutputs(mount model.LibraryMount, providerPath string) (int, error) {
	providerPath = normalizeProviderPath(providerPath)
	targetDir := a.mountTargetDirForProviderDir(mount, path.Dir(providerPath))
	if err := ensureOutputPathWithinRoot(a.config.Storage.STRMOutputDir, targetDir); err != nil {
		return 0, err
	}
	deleted := 0
	if isMediaFile(path.Base(providerPath)) {
		paths := a.mediaOutputPaths(mount, providerPath)
		strmPath := filepath.Join(paths.TargetDir, paths.BaseName+".strm")
		count, err := removeFileIfExists(strmPath)
		if err != nil {
			return deleted, err
		}
		deleted += count
		count, err = removeMediaCompanionOutputs(paths)
		if err != nil {
			return deleted, err
		}
		deleted += count
		return deleted, nil
	}

	exactPath := filepath.Join(targetDir, path.Base(providerPath))
	count, err := removeFileIfExists(exactPath)
	if err != nil {
		return deleted, err
	}
	return deleted + count, nil
}

func ensureOutputPathWithinRoot(outputRoot, targetPath string) error {
	cleanRoot := filepath.Clean(outputRoot)
	cleanTarget := filepath.Clean(targetPath)
	if !pathWithinRoot(cleanTarget, cleanRoot) {
		return fmt.Errorf("refuse to clean output path outside strm output dir: %s", cleanTarget)
	}
	return nil
}

func findLibraryMountByID(mounts []model.LibraryMount, mountID string) (model.LibraryMount, bool) {
	for _, mount := range mounts {
		if mount.ID == mountID {
			return mount, true
		}
	}
	return model.LibraryMount{}, false
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

func sourcePathForTargetPath(mounts []model.LibraryMount, targetPath string) (string, bool) {
	var selected model.LibraryMount
	selectedLen := -1
	normalizedTargetPath := normalizeProviderPath(targetPath)
	for _, mount := range mounts {
		mountTargetPath := normalizeProviderPath(mount.TargetPath)
		if !providerPathWithinRoot(normalizedTargetPath, mountTargetPath) {
			continue
		}
		if len(mountTargetPath) > selectedLen {
			selected = mount
			selectedLen = len(mountTargetPath)
		}
	}
	if selectedLen < 0 {
		return "", false
	}

	selectedTargetPath := normalizeProviderPath(selected.TargetPath)
	relToMount := ""
	if selectedTargetPath == "/" {
		relToMount = strings.TrimPrefix(normalizedTargetPath, "/")
	} else {
		relToMount = strings.TrimPrefix(normalizedTargetPath, selectedTargetPath)
		relToMount = strings.TrimPrefix(relToMount, "/")
	}
	if relToMount == "" {
		return normalizeProviderPath(selected.SourcePath), true
	}
	return normalizeProviderPath(path.Join(normalizeProviderPath(selected.SourcePath), relToMount)), true
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

func removeMediaCompanionOutputs(paths mediaOutputPaths) (int, error) {
	items, err := os.ReadDir(paths.TargetDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read output dir %s: %w", paths.TargetDir, err)
	}
	deleted := 0
	baseNameLower := strings.ToLower(paths.BaseName)
	for _, item := range items {
		if item.IsDir() {
			continue
		}
		name := item.Name()
		if !isMediaSpecificCompanion(baseNameLower, strings.ToLower(name)) {
			continue
		}
		count, err := removeFileIfExists(filepath.Join(paths.TargetDir, name))
		if err != nil {
			return deleted, err
		}
		deleted += count
	}
	return deleted, nil
}

func isMediaSpecificCompanion(baseNameLower, nameLower string) bool {
	if nameLower == baseNameLower+".nfo" {
		return true
	}
	if isSubtitleSidecar(baseNameLower, nameLower) {
		return true
	}
	if isMediaSpecificBIFSidecar(baseNameLower, nameLower) {
		return true
	}
	if nameLower == baseNameLower+".mediainfo.json" || nameLower == baseNameLower+"-mediainfo.json" {
		return true
	}
	if _, ok := imageExtensions[strings.ToLower(filepath.Ext(nameLower))]; !ok {
		return false
	}
	stem := strings.TrimSuffix(nameLower, strings.ToLower(filepath.Ext(nameLower)))
	return stem == baseNameLower || strings.HasPrefix(stem, baseNameLower+".") || strings.HasPrefix(stem, baseNameLower+"-")
}

func isMediaSpecificBIFSidecar(baseNameLower, nameLower string) bool {
	if strings.ToLower(filepath.Ext(nameLower)) != ".bif" {
		return false
	}
	stem := strings.TrimSuffix(nameLower, ".bif")
	return stem == baseNameLower || strings.HasPrefix(stem, baseNameLower+".") || strings.HasPrefix(stem, baseNameLower+"-")
}

func removeFileIfExists(filePath string) (int, error) {
	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("remove output file %s: %w", filePath, err)
	}
	return 1, nil
}

func cleanupStaleSTRMCurrentDir(targetRoot string, expected map[string]struct{}) (int, error) {
	items, err := os.ReadDir(targetRoot)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read strm dir %s: %w", targetRoot, err)
	}
	deleted := 0
	for _, item := range items {
		if item.IsDir() || strings.ToLower(filepath.Ext(item.Name())) != ".strm" {
			continue
		}
		current := filepath.Clean(filepath.Join(targetRoot, item.Name()))
		if _, ok := expected[current]; ok {
			continue
		}
		if err := os.Remove(current); err != nil {
			return deleted, fmt.Errorf("remove stale strm %s: %w", current, err)
		}
		deleted++
	}
	return deleted, nil
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

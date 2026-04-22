package app

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"time"

	"emby115/internal/config"
	"emby115/internal/storage"
)

type App struct {
	config     config.Config
	db         *sql.DB
	httpServer *http.Server
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
		config: cfg,
		db:     db,
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

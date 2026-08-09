package app

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"NyaMedia/internal/model"
	"NyaMedia/internal/storage"

	_ "modernc.org/sqlite"
)

func TestHandleStreamUsesRootedLocalFileAndRejectsDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := newStreamTestApp(t, root, true)

	fileRecorder := httptest.NewRecorder()
	a.handleStream(fileRecorder, httptest.NewRequest(http.MethodGet, "/stream/provider-a/file.txt", nil))
	if fileRecorder.Code != http.StatusOK || fileRecorder.Body.String() != "content" {
		t.Fatalf("file response status=%d body=%q", fileRecorder.Code, fileRecorder.Body.String())
	}

	dirRecorder := httptest.NewRecorder()
	a.handleStream(dirRecorder, httptest.NewRequest(http.MethodGet, "/stream/provider-a/directory", nil))
	if dirRecorder.Code != http.StatusNotFound {
		t.Fatalf("directory response status=%d, want 404", dirRecorder.Code)
	}
}

func TestHandleStreamRejectsDisabledLocalProvider(t *testing.T) {
	a := newStreamTestApp(t, t.TempDir(), false)
	recorder := httptest.NewRecorder()
	a.handleStream(recorder, httptest.NewRequest(http.MethodGet, "/stream/provider-a/file.txt", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("disabled provider response status=%d, want 404", recorder.Code)
	}
}

func TestStreamPlaybackModeUsesProviderDefaultAndRequestOverride(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/stream/provider-a/file.mkv", nil)
	redirectProvider := model.Provider{}
	if got := streamPlaybackMode(request, redirectProvider); got != model.PlaybackModeRedirect {
		t.Fatalf("default playback mode = %q, want %q", got, model.PlaybackModeRedirect)
	}

	proxyProvider := model.Provider{ConfigJSON: `{"playback_mode":"proxy"}`}
	if got := streamPlaybackMode(request, proxyProvider); got != model.PlaybackModeProxy {
		t.Fatalf("provider playback mode = %q, want %q", got, model.PlaybackModeProxy)
	}

	request = httptest.NewRequest(http.MethodGet, "/stream/provider-a/file.mkv?mode=redirect", nil)
	if got := streamPlaybackMode(request, proxyProvider); got != model.PlaybackModeRedirect {
		t.Fatalf("explicit redirect playback mode = %q, want %q", got, model.PlaybackModeRedirect)
	}

	request = httptest.NewRequest(http.MethodGet, "/stream/provider-a/file.mkv?mode=proxy", nil)
	if got := streamPlaybackMode(request, redirectProvider); got != model.PlaybackModeProxy {
		t.Fatalf("explicit proxy playback mode = %q, want %q", got, model.PlaybackModeProxy)
	}

	invalidProvider := model.Provider{ConfigJSON: `{"playback_mode":"invalid"}`}
	if got := streamPlaybackMode(httptest.NewRequest(http.MethodGet, "/stream/provider-a/file.mkv", nil), invalidProvider); got != model.PlaybackModeRedirect {
		t.Fatalf("invalid provider playback mode = %q, want %q", got, model.PlaybackModeRedirect)
	}
}

func newStreamTestApp(t *testing.T, rootPath string, enabled bool) *App {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	const schema = `
CREATE TABLE providers (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    root_path TEXT NOT NULL,
    status TEXT NOT NULL,
    last_check_at TEXT,
    last_error TEXT,
    config_json TEXT,
    enabled INTEGER NOT NULL,
    watch_enabled INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	repository := storage.NewProviderRepository(db)
	if err := repository.Create(t.Context(), model.Provider{
		ID:       "provider-a",
		Type:     "local",
		Name:     "Local",
		RootPath: rootPath,
		Enabled:  enabled,
	}); err != nil {
		t.Fatal(err)
	}
	return &App{providers: repository}
}

package app

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"NyaMedia/internal/config"
	"NyaMedia/internal/model"
	provideriface "NyaMedia/internal/provider"
)

func TestWebhookScanPath(t *testing.T) {
	isDir := true
	isFile := false
	tests := []struct {
		name  string
		path  string
		isDir *bool
		want  string
	}{
		{name: "file path scans parent", path: "/Movies/A/movie.mkv", isDir: &isFile, want: "/Movies/A"},
		{name: "directory path scans itself", path: "/Movies/A", isDir: &isDir, want: "/Movies/A"},
		{name: "unknown kind scans parent", path: "/Movies/A/movie.mkv", want: "/Movies/A"},
		{name: "root child file scans root", path: "/movie.mkv", want: "/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := webhookScanPath(tt.path, tt.isDir); got != tt.want {
				t.Fatalf("webhookScanPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecodeFilesystemWebhookScanMode(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantMode  string
		wantError bool
	}{
		{name: "defaults to current level", body: `{"source_path":"/Shows"}`, wantMode: scanQueueModeCurrentLevel},
		{name: "accepts recursive", body: `{"source_path":"/Shows","scan_mode":"recursive"}`, wantMode: scanQueueModeRecursive},
		{name: "accepts camel case alias", body: `{"source_path":"/Shows","scanMode":"CURRENT_LEVEL"}`, wantMode: scanQueueModeCurrentLevel},
		{name: "rejects unsupported mode", body: `{"source_path":"/Shows","scan_mode":"deep"}`, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/v1/webhooks/filesystem", strings.NewReader(tt.body))
			payload, _, err := decodeFilesystemWebhook(req)
			if tt.wantError {
				if err == nil {
					t.Fatal("decodeFilesystemWebhook() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if payload.ScanMode != tt.wantMode {
				t.Fatalf("scan mode = %q, want %q", payload.ScanMode, tt.wantMode)
			}
		})
	}
}

func TestWebhookScanPathForDeleteReconcilesParent(t *testing.T) {
	isDir := true
	tests := []struct {
		name      string
		path      string
		event     string
		mountRoot string
		want      string
	}{
		{name: "changed directory scans itself", path: "/Shows/Series/Season 1", event: "change", mountRoot: "/Shows", want: "/Shows/Series/Season 1"},
		{name: "deleted directory scans parent", path: "/Shows/Series/Season 1", event: "delete", mountRoot: "/Shows", want: "/Shows/Series"},
		{name: "deleted mount root stays within mount", path: "/Shows", event: "delete", mountRoot: "/Shows", want: "/Shows"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := webhookScanPathForEvent(tt.path, &isDir, tt.event, tt.mountRoot)
			if got != tt.want {
				t.Fatalf("webhookScanPathForEvent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStringFromMapAliases(t *testing.T) {
	values := map[string]any{"providerPath": " /Movies/A/movie.mkv "}
	if got := stringFromMap(values, "source_path", "providerPath"); got != "/Movies/A/movie.mkv" {
		t.Fatalf("stringFromMap() = %q", got)
	}
}

func TestWebhookPayloadPathsIncludesDestination(t *testing.T) {
	paths := webhookPayloadPaths(filesystemWebhookPayload{
		SourcePath:      "/Movies/Old/movie.mkv",
		DestinationPath: "/Movies/New/movie.mkv",
	})
	if len(paths) != 2 {
		t.Fatalf("len(paths) = %d, want 2", len(paths))
	}
	if paths[0] != "/Movies/Old/movie.mkv" || paths[1] != "/Movies/New/movie.mkv" {
		t.Fatalf("paths = %#v", paths)
	}
}

func TestWebhookPayloadPathsStripsConfiguredPrefix(t *testing.T) {
	paths := webhookPayloadPathsForPrefixes(filesystemWebhookPayload{
		SourcePath:      "/115open/Video/TV/Anime/movie.mkv",
		DestinationPath: "/115open/Video/TV/Anime/movie.nfo",
	}, []string{"/115open"})
	if len(paths) != 2 {
		t.Fatalf("len(paths) = %d, want 2", len(paths))
	}
	if paths[0] != "/Video/TV/Anime/movie.mkv" || paths[1] != "/Video/TV/Anime/movie.nfo" {
		t.Fatalf("paths = %#v", paths)
	}
}

func TestWebhookPayloadPathsPreservesProviderPathsWithoutPrefixes(t *testing.T) {
	paths := webhookPayloadPathsForPrefixes(filesystemWebhookPayload{
		SourcePath:      "/Video/TV/Anime/movie.mkv",
		DestinationPath: "/Video/TV/Anime/movie.nfo",
	}, nil)
	if len(paths) != 2 {
		t.Fatalf("len(paths) = %d, want 2", len(paths))
	}
	if paths[0] != "/Video/TV/Anime/movie.mkv" || paths[1] != "/Video/TV/Anime/movie.nfo" {
		t.Fatalf("paths = %#v", paths)
	}
}

func TestStripProviderPathPrefixesIgnoresPartialSegment(t *testing.T) {
	if got, ok := stripProviderPathPrefixes("/115open2/Video/movie.mkv", []string{"/115open"}); ok || got != "" {
		t.Fatalf("stripProviderPathPrefixes() = %q, %v", got, ok)
	}
}

func TestProviderWebhookPathPrefixes(t *testing.T) {
	prefixes := providerWebhookPathPrefixes(model.Provider{ConfigJSON: `{"webhook":{"path_prefixes":["/115open","/115open/"]}}`})
	if len(prefixes) != 1 || prefixes[0] != "/115open" {
		t.Fatalf("prefixes = %#v", prefixes)
	}
}

func TestWebhookPayloadPathsWithPrefixesIgnoresUnboundPath(t *testing.T) {
	paths := webhookPayloadPathsForPrefixes(filesystemWebhookPayload{SourcePath: "/local/Video/movie.mkv"}, []string{"/115open"})
	if len(paths) != 0 {
		t.Fatalf("paths = %#v, want empty", paths)
	}
}

func TestWebhookPayloadPathsWithPrefixesKeepsProviderBinding(t *testing.T) {
	paths := webhookPayloadPathsWithPrefixes(filesystemWebhookPayload{ProviderID: "provider-1", SourcePath: "/115open/Video/movie.mkv"}, []string{"/123pan"})
	if len(paths) != 0 {
		t.Fatalf("paths = %#v, want empty", paths)
	}
}

func TestIsWebhookDeleteEvent(t *testing.T) {
	for _, event := range []string{"delete", "deleted", "remove", "removed", "unlink", "unlinked"} {
		if !isWebhookDeleteEvent(event) {
			t.Fatalf("isWebhookDeleteEvent(%q) = false", event)
		}
	}
	for _, event := range []string{"create", "write", "change", "rename"} {
		if isWebhookDeleteEvent(event) {
			t.Fatalf("isWebhookDeleteEvent(%q) = true", event)
		}
	}
}

func TestCleanupStaleOutputsCurrentDirDoesNotRecurse(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "keep.strm")
	stale := filepath.Join(root, "stale.strm")
	staleNFO := filepath.Join(root, "tvshow.nfo")
	nestedDir := filepath.Join(root, "nested")
	nested := filepath.Join(nestedDir, "nested.strm")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleNFO, []byte("stale nfo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}

	deleted, err := cleanupStaleOutputsCurrentDir(root, root, map[string]struct{}{filepath.Clean(keep): {}})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("keep file missing: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale file still exists or unexpected error: %v", err)
	}
	if _, err := os.Stat(staleNFO); !os.IsNotExist(err) {
		t.Fatalf("stale nfo still exists or unexpected error: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("nested file should not be touched: %v", err)
	}
}

func TestIsMediaSpecificCompanionDoesNotMatchSharedArtwork(t *testing.T) {
	baseName := "episode01"
	for _, name := range []string{"episode01.nfo", "episode01.zh.srt", "episode01-commentary.srt", "episode01-poster.jpg", "episode01-thumb.jpg", "episode01.mediainfo.json"} {
		if !isMediaSpecificCompanion(baseName, name) {
			t.Fatalf("isMediaSpecificCompanion(%q) = false", name)
		}
	}
	for _, name := range []string{"poster.jpg", "folder.jpg", "mediainfo.json", "index.bif"} {
		if isMediaSpecificCompanion(baseName, name) {
			t.Fatalf("isMediaSpecificCompanion(%q) = true", name)
		}
	}
}

func TestBuildOutputSyncJobsIncludesEpisodeArtworkAndSubtitleVariants(t *testing.T) {
	a := &App{config: config.Config{Storage: config.StorageConfig{STRMOutputDir: t.TempDir()}}}
	mount := model.LibraryMount{SourcePath: "/Video/TV", TargetPath: "/TV"}
	media := provideriface.Entry{Name: "Episode 01.mkv", Path: "/Video/TV/Show/Season 01/Episode 01.mkv"}
	downloads := providerDownloadOptions{Images: true, Subtitles: true, BIF: true}

	jobs, err := a.buildOutputSyncJobs(mount, media, []provideriface.Entry{
		media,
		{Name: "Episode 01-thumb.jpg", Path: "/Video/TV/Show/Season 01/Episode 01-thumb.jpg"},
		{Name: "Episode 01.zh.srt", Path: "/Video/TV/Show/Season 01/Episode 01.zh.srt"},
		{Name: "Episode 01-commentary.srt", Path: "/Video/TV/Show/Season 01/Episode 01-commentary.srt"},
		{Name: "Episode 01-preview.bif", Path: "/Video/TV/Show/Season 01/Episode 01-preview.bif"},
	}, downloads)
	if err != nil {
		t.Fatalf("build output sync jobs: %v", err)
	}

	if len(jobs) != 4 {
		t.Fatalf("len(jobs) = %d, want 4", len(jobs))
	}
	for _, job := range jobs {
		if filepath.Dir(job.TargetPath) != filepath.Join(a.config.Storage.STRMOutputDir, "TV", "Show", "Season 01") {
			t.Fatalf("job target path = %q", job.TargetPath)
		}
	}
}

func TestBuildDirectoryOutputSyncJobsIncludesShowAndSeasonMetadata(t *testing.T) {
	a := &App{config: config.Config{Storage: config.StorageConfig{STRMOutputDir: t.TempDir()}}}
	mount := model.LibraryMount{SourcePath: "/Video/TV", TargetPath: "/TV"}
	downloads := providerDownloadOptions{NFO: true, Images: true}

	jobs, err := a.buildDirectoryOutputSyncJobs(mount, "/Video/TV/Show/Season 01", []provideriface.Entry{
		{Name: "season.nfo", Path: "/Video/TV/Show/Season 01/season.nfo"},
		{Name: "poster.jpg", Path: "/Video/TV/Show/Season 01/poster.jpg"},
		{Name: "season01-poster.jpg", Path: "/Video/TV/Show/Season 01/season01-poster.jpg"},
		{Name: "Episode 01.nfo", Path: "/Video/TV/Show/Season 01/Episode 01.nfo"},
	}, downloads)
	if err != nil {
		t.Fatalf("build directory output sync jobs: %v", err)
	}

	if len(jobs) != 3 {
		t.Fatalf("len(jobs) = %d, want 3", len(jobs))
	}
	wantTargetDir := filepath.Join(a.config.Storage.STRMOutputDir, "TV", "Show", "Season 01")
	for _, job := range jobs {
		if filepath.Dir(job.TargetPath) != wantTargetDir {
			t.Fatalf("job target dir = %q, want %q", filepath.Dir(job.TargetPath), wantTargetDir)
		}
	}
	if jobs[0].SourcePath != "/Video/TV/Show/Season 01/season.nfo" {
		t.Fatalf("first job source = %q", jobs[0].SourcePath)
	}
	if jobs[1].SourcePath != "/Video/TV/Show/Season 01/poster.jpg" {
		t.Fatalf("second job source = %q", jobs[1].SourcePath)
	}
	if jobs[2].SourcePath != "/Video/TV/Show/Season 01/season01-poster.jpg" {
		t.Fatalf("third job source = %q", jobs[2].SourcePath)
	}
}

func TestBuildDirectoryOutputSyncJobsIncludesTVShowMetadata(t *testing.T) {
	a := &App{config: config.Config{Storage: config.StorageConfig{STRMOutputDir: t.TempDir()}}}
	mount := model.LibraryMount{SourcePath: "/Video/TV", TargetPath: "/TV"}
	downloads := providerDownloadOptions{NFO: true, Images: true}

	jobs, err := a.buildDirectoryOutputSyncJobs(mount, "/Video/TV/Show", []provideriface.Entry{
		{Name: "tvshow.nfo", Path: "/Video/TV/Show/tvshow.nfo"},
		{Name: "fanart.webp", Path: "/Video/TV/Show/fanart.webp"},
	}, downloads)
	if err != nil {
		t.Fatalf("build directory output sync jobs: %v", err)
	}

	if len(jobs) != 2 {
		t.Fatalf("len(jobs) = %d, want 2", len(jobs))
	}
	wantTargetDir := filepath.Join(a.config.Storage.STRMOutputDir, "TV", "Show")
	for _, job := range jobs {
		if filepath.Dir(job.TargetPath) != wantTargetDir {
			t.Fatalf("job target dir = %q, want %q", filepath.Dir(job.TargetPath), wantTargetDir)
		}
	}
}

func TestIsBIFSidecarMatchesCommonEpisodeVariants(t *testing.T) {
	baseName := "show.s01e01"
	for _, name := range []string{"show.s01e01.bif", "show.s01e01-preview.bif", "show.s01e01.320x180.bif", "index.bif"} {
		if !isBIFSidecar(baseName, name) {
			t.Fatalf("isBIFSidecar(%q) = false", name)
		}
	}
	if isBIFSidecar(baseName, "show.s01e02.bif") {
		t.Fatalf("isBIFSidecar() matched another episode")
	}
}

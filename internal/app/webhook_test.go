package app

import (
	"os"
	"path/filepath"
	"testing"
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

func TestCleanupStaleSTRMCurrentDirDoesNotRecurse(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "keep.strm")
	stale := filepath.Join(root, "stale.strm")
	nestedDir := filepath.Join(root, "nested")
	nested := filepath.Join(nestedDir, "nested.strm")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}

	deleted, err := cleanupStaleSTRMCurrentDir(root, map[string]struct{}{filepath.Clean(keep): {}})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("keep file missing: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale file still exists or unexpected error: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("nested file should not be touched: %v", err)
	}
}

func TestIsMediaSpecificCompanionDoesNotMatchSharedArtwork(t *testing.T) {
	baseName := "episode01"
	for _, name := range []string{"episode01.nfo", "episode01.zh.srt", "episode01-poster.jpg", "episode01.mediainfo.json"} {
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

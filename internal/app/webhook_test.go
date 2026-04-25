package app

import "testing"

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

package app

import (
	"testing"

	"emby115/internal/config"
)

func TestBuildEmbyPlaybackInfoPath(t *testing.T) {
	tests := []struct {
		name      string
		remainder string
		want      string
		wantOK    bool
	}{
		{
			name:      "emby original video path",
			remainder: "/emby/videos/10367/original.mkv",
			want:      "/emby/Items/10367/PlaybackInfo",
			wantOK:    true,
		},
		{
			name:      "non playback path",
			remainder: "/emby/web/index.html",
			wantOK:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := buildEmbyPlaybackInfoPath(tt.remainder)
			if ok != tt.wantOK {
				t.Fatalf("buildEmbyPlaybackInfoPath() ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("buildEmbyPlaybackInfoPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractManagedPlaybackURL(t *testing.T) {
	app := &App{config: config.Config{Server: config.ServerConfig{PublicBaseURL: "http://127.0.0.1:7001"}}}
	body := []byte(`{"MediaSources":[{"Path":"/stream/provider-a/folder/movie.mkv"}]}`)

	got, ok, err := app.extractManagedPlaybackURL(body)
	if err != nil {
		t.Fatalf("extractManagedPlaybackURL() error = %v", err)
	}
	if !ok {
		t.Fatalf("extractManagedPlaybackURL() ok = false, want true")
	}
	if got != "http://127.0.0.1:7001/stream/provider-a/folder/movie.mkv" {
		t.Fatalf("extractManagedPlaybackURL() = %q", got)
	}
}

func TestNormalizeEmbyProxyBasePath(t *testing.T) {
	tests := []struct {
		name      string
		pathValue string
		want      string
		wantOK    bool
	}{
		{
			name:      "proxy base without trailing slash",
			pathValue: "/proxy/home",
			want:      "/proxy/home/",
			wantOK:    true,
		},
		{
			name:      "proxy base with trailing slash",
			pathValue: "/proxy/home/",
			wantOK:    false,
		},
		{
			name:      "nested proxy path",
			pathValue: "/proxy/home/web/index.html",
			wantOK:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := normalizeEmbyProxyBasePath(tt.pathValue)
			if ok != tt.wantOK {
				t.Fatalf("normalizeEmbyProxyBasePath() ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("normalizeEmbyProxyBasePath() = %q, want %q", got, tt.want)
			}
		})
	}
}

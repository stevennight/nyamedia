package app

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"emby115/internal/config"
)

func TestRewriteEmbyPlaybackInfoBody(t *testing.T) {
	body := []byte(`{"MediaSources":[{"Id":"1","Path":"/stream/provider-a/folder%20name/movie.mkv","DirectStreamUrl":"Videos/1/stream.mkv?static=true"},{"Id":"2","Path":"https://upstream.example/media/file.mkv"}],"TranscodingUrl":"https://upstream.example/emby/Videos/1/master.m3u8"}`)

	app := &App{config: config.Config{Server: config.ServerConfig{PublicBaseURL: "https://public.example/base"}}}
	target, err := url.Parse("https://upstream.example/emby")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	rewritten, changed, err := rewriteEmbyPlaybackInfoBody(context.Background(), body, func(ctx context.Context, pathValue string) (string, bool, error) {
		return app.rewriteManagedPlaybackPath(ctx, pathValue)
	}, func(pathValue string) (string, bool, error) {
		return app.rewriteEmbyProxyURL("main", target, pathValue)
	})
	if err != nil {
		t.Fatalf("rewriteEmbyPlaybackInfoBody() error = %v", err)
	}
	if !changed {
		t.Fatalf("rewriteEmbyPlaybackInfoBody() changed = false, want true")
	}

	var payload map[string]any
	if err := json.Unmarshal(rewritten, &payload); err != nil {
		t.Fatalf("unmarshal rewritten body: %v", err)
	}
	mediaSources, ok := payload["MediaSources"].([]any)
	if !ok || len(mediaSources) != 2 {
		t.Fatalf("MediaSources = %#v", payload["MediaSources"])
	}
	first, _ := mediaSources[0].(map[string]any)
	second, _ := mediaSources[1].(map[string]any)
	if got := first["Path"]; got != "https://public.example/base/stream/provider-a/folder%20name/movie.mkv" {
		t.Fatalf("first path = %v, want rewritten service url", got)
	}
	if got := first["DirectStreamUrl"]; got != "/base/proxy/main/Videos/1/stream.mkv?static=true" {
		t.Fatalf("first direct stream url = %v, want rewritten proxy url", got)
	}
	if got := second["Path"]; got != "https://upstream.example/media/file.mkv" {
		t.Fatalf("second path = %v, want unchanged", got)
	}
	if got := payload["TranscodingUrl"]; got != "/base/proxy/main/Videos/1/master.m3u8" {
		t.Fatalf("transcoding url = %v, want rewritten proxy url", got)
	}
}

func TestRewriteManagedPlaybackPath(t *testing.T) {
	app := &App{config: config.Config{Server: config.ServerConfig{PublicBaseURL: "https://public.example/base"}}}

	got, ok, err := app.rewriteManagedPlaybackPath(context.Background(), "/stream/provider-a/folder/file.mkv?foo=bar")
	if err != nil {
		t.Fatalf("rewriteManagedPlaybackPath() error = %v", err)
	}
	if !ok {
		t.Fatalf("rewriteManagedPlaybackPath() ok = false, want true")
	}
	if got != "https://public.example/base/stream/provider-a/folder/file.mkv?foo=bar" {
		t.Fatalf("rewriteManagedPlaybackPath() = %q", got)
	}
}

func TestParseManagedStreamPath(t *testing.T) {
	app := &App{config: config.Config{Server: config.ServerConfig{PublicBaseURL: "https://public.example/base"}}}

	tests := []struct {
		name         string
		pathValue    string
		wantProvider string
		wantPath     string
		wantOK       bool
	}{
		{
			name:         "absolute public stream url",
			pathValue:    "https://public.example/base/stream/provider-a/folder%20name/movie.mkv?mode=proxy",
			wantProvider: "provider-a",
			wantPath:     "/folder name/movie.mkv",
			wantOK:       true,
		},
		{
			name:         "relative stream url",
			pathValue:    "/stream/provider-b/dir/file.mp4",
			wantProvider: "provider-b",
			wantPath:     "/dir/file.mp4",
			wantOK:       true,
		},
		{
			name:      "non managed url",
			pathValue: "https://other.example/stream/provider-a/file.mkv",
			wantOK:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotProvider, gotPath, gotOK := app.parseManagedStreamPath(tt.pathValue)
			if gotOK != tt.wantOK {
				t.Fatalf("parseManagedStreamPath() ok = %v, want %v", gotOK, tt.wantOK)
			}
			if gotProvider != tt.wantProvider {
				t.Fatalf("parseManagedStreamPath() provider = %q, want %q", gotProvider, tt.wantProvider)
			}
			if gotPath != tt.wantPath {
				t.Fatalf("parseManagedStreamPath() path = %q, want %q", gotPath, tt.wantPath)
			}
		})
	}
}

func TestRewriteEmbyProxyURL(t *testing.T) {
	app := &App{config: config.Config{Server: config.ServerConfig{PublicBaseURL: "https://public.example/base"}}}
	target, err := url.Parse("https://upstream.example/emby")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	tests := []struct {
		name      string
		pathValue string
		want      string
		wantOK    bool
	}{
		{
			name:      "absolute upstream url",
			pathValue: "https://upstream.example/emby/Videos/1/master.m3u8?segment=1",
			want:      "/base/proxy/main/Videos/1/master.m3u8?segment=1",
			wantOK:    true,
		},
		{
			name:      "relative playback url",
			pathValue: "Videos/1/stream.mkv?static=true",
			want:      "/base/proxy/main/Videos/1/stream.mkv?static=true",
			wantOK:    true,
		},
		{
			name:      "external url",
			pathValue: "https://cdn.example/video.mkv",
			wantOK:    false,
		},
		{
			name:      "already proxied public url",
			pathValue: "https://public.example/base/proxy/main/Videos/1/master.m3u8?segment=1",
			want:      "/base/proxy/main/Videos/1/master.m3u8?segment=1",
			wantOK:    true,
		},
		{
			name:      "managed stream public url",
			pathValue: "https://public.example/base/stream/provider-a/file.mkv",
			want:      "/base/stream/provider-a/file.mkv",
			wantOK:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := app.rewriteEmbyProxyURL("main", target, tt.pathValue)
			if err != nil {
				t.Fatalf("rewriteEmbyProxyURL() error = %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("rewriteEmbyProxyURL() ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("rewriteEmbyProxyURL() = %q, want %q", got, tt.want)
			}
		})
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

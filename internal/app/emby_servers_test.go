package app

import (
	"encoding/json"
	"testing"

	"NyaMedia/internal/config"
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

func TestParseEmbySubtitleStreamRequest(t *testing.T) {
	got, ok := parseEmbySubtitleStreamRequest("/emby/Videos/10370/d8475b756e06a7a46802718e5ba0e5da/Subtitles/12/0/Stream.ass")
	if !ok {
		t.Fatalf("parseEmbySubtitleStreamRequest() ok = false, want true")
	}
	if got.Prefix != "/emby" || got.ItemID != "10370" || got.MediaSourceID != "d8475b756e06a7a46802718e5ba0e5da" || got.StreamIndex != 12 {
		t.Fatalf("parseEmbySubtitleStreamRequest() = %+v", got)
	}
	if got.playbackInfoPath() != "/emby/Items/10370/PlaybackInfo" {
		t.Fatalf("playbackInfoPath() = %q", got.playbackInfoPath())
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

func TestHasRemoteEmbyMediaSource(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want bool
	}{
		{
			name: "strm media source is remote",
			body: []byte(`{"MediaSources":[{"Path":"/stream/provider-a/folder/movie.mkv","IsRemote":true}]}`),
			want: true,
		},
		{
			name: "local media source is not remote",
			body: []byte(`{"MediaSources":[{"Path":"D:\\Media\\movie.mkv","IsRemote":false}]}`),
			want: false,
		},
		{
			name: "missing media sources is not remote",
			body: []byte(`{}`),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hasRemoteEmbyMediaSource(tt.body)
			if err != nil {
				t.Fatalf("hasRemoteEmbyMediaSource() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("hasRemoteEmbyMediaSource() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAllowedSubtitleStream(t *testing.T) {
	body := []byte(`{"MediaSources":[{"Id":"remote-source","IsRemote":true,"MediaStreams":[{"Type":"Subtitle","Index":3,"IsExternal":false},{"Type":"Subtitle","Index":12,"IsExternal":true}]},{"Id":"local-source","IsRemote":false,"MediaStreams":[{"Type":"Subtitle","Index":2,"IsExternal":false}]}]}`)

	tests := []struct {
		name          string
		mediaSourceID string
		streamIndex   int
		want          bool
	}{
		{name: "remote external subtitle allowed", mediaSourceID: "remote-source", streamIndex: 12, want: true},
		{name: "remote internal subtitle rejected", mediaSourceID: "remote-source", streamIndex: 3, want: false},
		{name: "remote missing subtitle rejected", mediaSourceID: "remote-source", streamIndex: 99, want: false},
		{name: "local subtitle allowed", mediaSourceID: "local-source", streamIndex: 2, want: true},
		{name: "missing media source rejected", mediaSourceID: "missing-source", streamIndex: 2, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := isAllowedSubtitleStream(body, tt.mediaSourceID, tt.streamIndex)
			if err != nil {
				t.Fatalf("isAllowedSubtitleStream() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("isAllowedSubtitleStream() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilterRemoteMediaSourceEmbeddedSubtitles(t *testing.T) {
	body := []byte(`{"MediaSources":[{"IsRemote":true,"MediaStreams":[{"Type":"Video","Index":0,"IsExternal":false},{"Type":"Audio","Index":1,"IsExternal":false},{"Type":"Subtitle","Index":3,"IsExternal":false,"SubtitleLocationType":"InternalStream"},{"Type":"Subtitle","Index":12,"IsExternal":true,"Path":"D:\\Media\\movie.ass"},{"Type":"Attachment","Index":4,"Path":"font.otf"},{"Type":"Data","Index":5}]},{"IsRemote":false,"MediaStreams":[{"Type":"Subtitle","Index":2,"IsExternal":false},{"Type":"Attachment","Index":4}]}]}`)

	filtered, changed, err := filterRemoteMediaSourceEmbeddedSubtitles(body)
	if err != nil {
		t.Fatalf("filterRemoteMediaSourceEmbeddedSubtitles() error = %v", err)
	}
	if !changed {
		t.Fatalf("filterRemoteMediaSourceEmbeddedSubtitles() changed = false, want true")
	}

	var payload struct {
		MediaSources []struct {
			MediaStreams []struct {
				Type       string `json:"Type"`
				Index      int    `json:"Index"`
				IsExternal bool   `json:"IsExternal"`
			} `json:"MediaStreams"`
		} `json:"MediaSources"`
	}
	if err := json.Unmarshal(filtered, &payload); err != nil {
		t.Fatalf("decode filtered payload: %v", err)
	}

	remoteStreams := payload.MediaSources[0].MediaStreams
	if len(remoteStreams) != 3 {
		t.Fatalf("remote stream count = %d, want 3", len(remoteStreams))
	}
	for _, stream := range remoteStreams {
		if stream.Type == "Subtitle" && !stream.IsExternal {
			t.Fatalf("internal subtitle was not removed: index %d", stream.Index)
		}
		if stream.Type != "Video" && stream.Type != "Audio" && !(stream.Type == "Subtitle" && stream.IsExternal) {
			t.Fatalf("unsafe remote stream was not removed: type %s index %d", stream.Type, stream.Index)
		}
	}
	if len(payload.MediaSources[1].MediaStreams) != 2 {
		t.Fatalf("local media source stream count = %d, want 2", len(payload.MediaSources[1].MediaStreams))
	}
}

func TestFilterRemoteMediaSourceEmbeddedSubtitlesUnchanged(t *testing.T) {
	body := []byte(`{"MediaSources":[{"IsRemote":false,"MediaStreams":[{"Type":"Subtitle","Index":2,"IsExternal":false}]}]}`)

	filtered, changed, err := filterRemoteMediaSourceEmbeddedSubtitles(body)
	if err != nil {
		t.Fatalf("filterRemoteMediaSourceEmbeddedSubtitles() error = %v", err)
	}
	if changed {
		t.Fatalf("filterRemoteMediaSourceEmbeddedSubtitles() changed = true, want false")
	}
	if string(filtered) != string(body) {
		t.Fatalf("filterRemoteMediaSourceEmbeddedSubtitles() changed body unexpectedly")
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

func TestResolveUpstreamPath(t *testing.T) {
	tests := []struct {
		name      string
		basePath  string
		remainder string
		want      string
	}{
		{
			name:      "root upstream keeps request path",
			basePath:  "",
			remainder: "/emby/Videos/10367/Subtitles/3/0/Stream.ass",
			want:      "/emby/Videos/10367/Subtitles/3/0/Stream.ass",
		},
		{
			name:      "base path does not duplicate emby prefix",
			basePath:  "/emby",
			remainder: "/emby/Videos/10367/Subtitles/3/0/Stream.ass",
			want:      "/emby/Videos/10367/Subtitles/3/0/Stream.ass",
		},
		{
			name:      "base path prefixes bare playback info path",
			basePath:  "/emby",
			remainder: "/Items/10367/PlaybackInfo",
			want:      "/emby/Items/10367/PlaybackInfo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveUpstreamPath(tt.basePath, tt.remainder)
			if got != tt.want {
				t.Fatalf("resolveUpstreamPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

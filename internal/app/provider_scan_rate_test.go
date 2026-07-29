package app

import (
	"testing"
	"time"

	"NyaMedia/internal/model"
)

func TestProviderScanRequestInterval(t *testing.T) {
	tests := []struct {
		name     string
		provider model.Provider
		want     time.Duration
	}{
		{
			name:     "open default",
			provider: model.Provider{Type: "115open"},
			want:     500 * time.Millisecond,
		},
		{
			name:     "open configured",
			provider: model.Provider{Type: "115open", ConfigJSON: `{"scan_request_interval_ms":750}`},
			want:     750 * time.Millisecond,
		},
		{
			name:     "open minimum",
			provider: model.Provider{Type: "115open", ConfigJSON: `{"scan_request_interval_ms":0}`},
			want:     250 * time.Millisecond,
		},
		{
			name:     "open maximum",
			provider: model.Provider{Type: "115open", ConfigJSON: `{"scan_request_interval_ms":20000}`},
			want:     10 * time.Second,
		},
		{
			name:     "123pan default",
			provider: model.Provider{Type: "123pan"},
			want:     500 * time.Millisecond,
		},
		{
			name:     "123pan configured",
			provider: model.Provider{Type: "123pan", ConfigJSON: `{"scan_request_interval_ms":1000}`},
			want:     time.Second,
		},
		{
			name:     "baiduopen default",
			provider: model.Provider{Type: "baiduopen"},
			want:     500 * time.Millisecond,
		},
		{
			name:     "baiduopen configured",
			provider: model.Provider{Type: "baiduopen", ConfigJSON: `{"scan_request_interval_ms":1250}`},
			want:     1250 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := providerScanRequestInterval(tt.provider); got != tt.want {
				t.Fatalf("providerScanRequestInterval() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestProvider115CookieRequestInterval(t *testing.T) {
	tests := []struct {
		name     string
		provider model.Provider
		wantMin  time.Duration
		wantMax  time.Duration
	}{
		{
			name:     "default",
			provider: model.Provider{Type: "115cookie"},
			wantMin:  2 * time.Second,
			wantMax:  5 * time.Second,
		},
		{
			name:     "configured range",
			provider: model.Provider{Type: "115cookie", ConfigJSON: `{"request_interval_min_seconds":3,"request_interval_max_seconds":9}`},
			wantMin:  3 * time.Second,
			wantMax:  9 * time.Second,
		},
		{
			name:     "fixed interval",
			provider: model.Provider{Type: "115cookie", ConfigJSON: `{"request_interval_min_seconds":4,"request_interval_max_seconds":4}`},
			wantMin:  4 * time.Second,
			wantMax:  4 * time.Second,
		},
		{
			name:     "minimum and inverted range",
			provider: model.Provider{Type: "115cookie", ConfigJSON: `{"request_interval_min_seconds":0,"request_interval_max_seconds":0}`},
			wantMin:  time.Second,
			wantMax:  time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMin, gotMax := provider115CookieRequestInterval(tt.provider)
			if gotMin != tt.wantMin || gotMax != tt.wantMax {
				t.Fatalf("provider115CookieRequestInterval() = %s-%s, want %s-%s", gotMin, gotMax, tt.wantMin, tt.wantMax)
			}
		})
	}
}

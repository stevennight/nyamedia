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
			name:     "cookie unchanged",
			provider: model.Provider{Type: "115cookie", ConfigJSON: `{"scan_request_interval_ms":500}`},
			want:     0,
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

package app

import "testing"

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

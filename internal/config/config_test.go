package config

import "testing"

func TestConfigValidateProxyBaseURLs(t *testing.T) {
	base := Config{Server: ServerConfig{Host: "127.0.0.1", Port: 7001}, Storage: StorageConfig{
		DatabaseURL:   "postgres://localhost/nyamedia",
		STRMOutputDir: "./data/strm",
	}}

	tests := []struct {
		name          string
		proxyBaseURLs []string
		wantErr       bool
	}{
		{name: "empty allowlist", wantErr: false},
		{name: "absolute proxy url", proxyBaseURLs: []string{"https://proxy.example.com/nyamedia"}, wantErr: false},
		{name: "relative proxy url", proxyBaseURLs: []string{"/nyamedia"}, wantErr: true},
		{name: "query string", proxyBaseURLs: []string{"https://proxy.example.com?token=abc"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			cfg.Server.ProxyBaseURLs = tt.proxyBaseURLs
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

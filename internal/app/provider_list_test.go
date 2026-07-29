package app

import (
	"net/http/httptest"
	"testing"

	"NyaMedia/internal/model"
)

func TestProviderStatusCheckRequested(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "default", url: "/api/v1/providers", want: true},
		{name: "enabled", url: "/api/v1/providers?check_status=true", want: true},
		{name: "disabled", url: "/api/v1/providers?check_status=false", want: false},
		{name: "disabled case insensitive", url: "/api/v1/providers?check_status=FALSE", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", test.url, nil)
			if got := providerStatusCheckRequested(request); got != test.want {
				t.Fatalf("providerStatusCheckRequested() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestToProviderModelDisablesUnsupportedWatcher(t *testing.T) {
	tests := []struct {
		name         string
		providerType string
		want         bool
	}{
		{name: "local keeps watcher", providerType: "local", want: true},
		{name: "123pan disables watcher", providerType: "123pan", want: false},
		{name: "115open disables watcher", providerType: "115open", want: false},
		{name: "baiduopen disables watcher", providerType: "baiduopen", want: false},
		{name: "115cookie disables watcher", providerType: "115cookie", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := toProviderModel(providerPayload{
				ID:           "provider-a",
				Type:         test.providerType,
				Name:         "Provider",
				RootPath:     "/",
				Enabled:      true,
				WatchEnabled: true,
				Status:       model.ProviderStatusUnknown,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.WatchEnabled != test.want {
				t.Fatalf("WatchEnabled = %t, want %t", got.WatchEnabled, test.want)
			}
		})
	}
}

func TestProviderSecretValuesComplete(t *testing.T) {
	tests := []struct {
		name         string
		providerType string
		secrets      map[string]string
		want         bool
	}{
		{name: "123pan missing credentials", providerType: "123pan", secrets: map[string]string{}, want: false},
		{name: "123pan client id only", providerType: "123pan", secrets: map[string]string{"client_id": "id"}, want: false},
		{name: "123pan client secret only", providerType: "123pan", secrets: map[string]string{"client_secret": "secret"}, want: false},
		{name: "123pan complete", providerType: "123pan", secrets: map[string]string{"client_id": "id", "client_secret": "secret"}, want: true},
		{name: "baiduopen app credentials only", providerType: "baiduopen", secrets: map[string]string{"client_id": "id", "client_secret": "secret"}, want: false},
		{name: "baiduopen access token", providerType: "baiduopen", secrets: map[string]string{"client_id": "id", "client_secret": "secret", "access_token": "access"}, want: true},
		{name: "baiduopen refresh token", providerType: "baiduopen", secrets: map[string]string{"client_id": "id", "client_secret": "secret", "refresh_token": "refresh"}, want: true},
		{name: "local needs no secrets", providerType: "local", secrets: map[string]string{}, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := providerSecretValuesComplete(test.providerType, test.secrets); got != test.want {
				t.Fatalf("providerSecretValuesComplete() = %t, want %t", got, test.want)
			}
		})
	}
}

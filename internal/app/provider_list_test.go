package app

import (
	"net/http/httptest"
	"testing"
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

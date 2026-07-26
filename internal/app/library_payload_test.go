package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLibraryPayloadAcceptsDeprecatedScanCron(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/libraries", strings.NewReader(
		`{"id":"library-a","name":"Library A","description":"test","enabled":true,"scan_cron":""}`,
	))

	var payload libraryPayload
	if err := decodeJSON(request, &payload); err != nil {
		t.Fatalf("decode legacy library payload: %v", err)
	}
	if payload.DeprecatedScanCron != "" {
		t.Fatalf("deprecated scan cron = %q, want empty", payload.DeprecatedScanCron)
	}

	library, err := toLibraryModel(payload)
	if err != nil {
		t.Fatalf("convert library payload: %v", err)
	}
	if library.ID != "library-a" || library.Name != "Library A" || !library.Enabled {
		t.Fatalf("unexpected library: %+v", library)
	}
}

package open115

import "testing"

func TestProviderHTTPClientRequestTimeout(t *testing.T) {
	provider := New("provider-a", "/", "", "", nil)
	if got := provider.httpClient.Timeout; got != requestTimeout {
		t.Fatalf("request timeout = %s, want %s", got, requestTimeout)
	}
}

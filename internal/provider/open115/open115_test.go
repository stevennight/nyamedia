package open115

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"NyaMedia/internal/model"
	provideriface "NyaMedia/internal/provider"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type memoryCacheStore struct {
	mu     sync.Mutex
	values map[string]string
}

func newMemoryCacheStore() *memoryCacheStore {
	return &memoryCacheStore{values: make(map[string]string)}
}

func (s *memoryCacheStore) Get(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[key]
	return value, ok, nil
}

func (s *memoryCacheStore) Set(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *memoryCacheStore) SetWithTTL(ctx context.Context, key, value string, _ time.Duration) error {
	return s.Set(ctx, key, value)
}

func TestProviderHTTPClientRequestTimeout(t *testing.T) {
	p := New("provider-a", "/", "", "", nil)
	if got := p.httpClient.Timeout; got != requestTimeout {
		t.Fatalf("request timeout = %s, want %s", got, requestTimeout)
	}
}

func TestListUsesPersistentChildrenCacheAndSupportsBypass(t *testing.T) {
	cache := newMemoryCacheStore()
	var requests atomic.Int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests.Add(1)
		return jsonResponse(http.StatusOK, `{
			"state": true,
			"code": 0,
			"data": [{"fid":"file-1","pid":"0","fc":"1","fn":"movie.mkv","pc":"pick-1","fs":123}],
			"count": 1,
			"offset": 0,
			"limit": 1000,
			"cid": 0
		}`), nil
	})

	first := New("provider-a", "/", "access", "refresh", nil, cache)
	first.httpClient.Transport = transport
	items, err := first.List(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "movie.mkv" {
		t.Fatalf("items = %+v", items)
	}

	second := New("provider-a", "/", "access", "refresh", nil, cache)
	second.httpClient.Transport = transport
	items, err = second.List(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("cached items = %+v", items)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests after cached list = %d, want 1", got)
	}

	if _, err := second.List(provideriface.WithBypassCache(context.Background()), "/"); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("requests after bypass = %d, want 2", got)
	}
}

func TestDirectLinkUsesRequestUserAgent(t *testing.T) {
	const userAgent = "NyaMedia-Test-UA"
	p := New("provider-a", "/", "access", "refresh", nil)
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/open/ufile/downurl" {
			return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
		}
		if got := req.Header.Get("User-Agent"); got != userAgent {
			return nil, fmt.Errorf("user-agent = %q, want %q", got, userAgent)
		}
		return jsonResponse(http.StatusOK, `{
			"state": true,
			"code": 0,
			"data": {"file-1":{"file_name":"movie.mkv","url":{"url":"https://download.example/movie.mkv"}}}
		}`), nil
	})

	ctx := provideriface.WithRequestUserAgent(context.Background(), userAgent)
	result, err := p.GetDirectLinkForEntry(ctx, provideriface.DirectLinkInput{
		Path: "/movie.mkv",
		Metadata: map[string]string{
			"pick_code": "pick-1",
			"mime_type": "application/octet-stream",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.URL != "https://download.example/movie.mkv" {
		t.Fatalf("url = %q", result.URL)
	}
	if result.Headers["User-Agent"] != userAgent {
		t.Fatalf("direct link headers = %+v", result.Headers)
	}
}

func TestScanRequestsRespectConfiguredInterval(t *testing.T) {
	const interval = 80 * time.Millisecond
	var requestTimes []time.Time
	var requestTimesMu sync.Mutex
	p := New("provider-a", "/", "access", "refresh", nil)
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestTimesMu.Lock()
		requestTimes = append(requestTimes, time.Now())
		requestTimesMu.Unlock()
		return jsonResponse(http.StatusOK, `{
			"state": true,
			"code": 0,
			"data": [],
			"count": 0,
			"offset": 0,
			"limit": 1000,
			"cid": 0
		}`), nil
	})

	ctx := provideriface.WithScanRequestInterval(context.Background(), interval)
	ctx = provideriface.WithBypassCache(ctx)
	if _, err := p.List(ctx, "/"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.List(ctx, "/"); err != nil {
		t.Fatal(err)
	}

	requestTimesMu.Lock()
	defer requestTimesMu.Unlock()
	if len(requestTimes) != 2 {
		t.Fatalf("request times = %d, want 2", len(requestTimes))
	}
	if elapsed := requestTimes[1].Sub(requestTimes[0]); elapsed < 60*time.Millisecond {
		t.Fatalf("request interval = %s, want at least 60ms", elapsed)
	}
}

func TestConcurrentUnauthorizedRequestsRefreshOnce(t *testing.T) {
	var refreshRequests atomic.Int32
	var callbackRequests atomic.Int32
	p := New("provider-a", "/", "old-access", "old-refresh", func(accessToken, refreshToken string) {
		if accessToken != "new-access" || refreshToken != "new-refresh" {
			t.Errorf("refreshed tokens = %q, %q", accessToken, refreshToken)
		}
		callbackRequests.Add(1)
	})
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/open/refreshToken" {
			refreshRequests.Add(1)
			return jsonResponse(http.StatusOK, `{
				"code": 0,
				"data": {"access_token":"new-access","refresh_token":"new-refresh","expires_in":7200}
			}`), nil
		}
		if req.Header.Get("Authorization") == "Bearer old-access" {
			return jsonResponse(http.StatusUnauthorized, `{"state":false,"code":99,"message":"expired"}`), nil
		}
		return jsonResponse(http.StatusOK, `{
			"state": true,
			"code": 0,
			"data": {"file_id":"0","file_name":"/","file_category":"0"}
		}`), nil
	})

	const workers = 8
	start := make(chan struct{})
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			var info infoResponse
			errs <- p.doAPI(context.Background(), http.MethodPost, apiBaseURL+"/open/folder/get_info", nil, map[string]string{"path": "/"}, &info)
		}()
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := refreshRequests.Load(); got != 1 {
		t.Fatalf("refresh requests = %d, want 1", got)
	}
	if got := callbackRequests.Load(); got != 1 {
		t.Fatalf("refresh callbacks = %d, want 1", got)
	}
}

func TestCheckStatusPerformsAuthenticatedRequest(t *testing.T) {
	var requests atomic.Int32
	p := New("provider-a", "/", "access", "refresh", nil)
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests.Add(1)
		return jsonResponse(http.StatusOK, `{
			"state": true,
			"code": 0,
			"data": [],
			"count": 0,
			"offset": 0,
			"limit": 1,
			"cid": 0
		}`), nil
	})

	status, message := p.CheckStatus(context.Background())
	if status != model.ProviderStatusHealthy || message != "" {
		t.Fatalf("status = %q, message = %q", status, message)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

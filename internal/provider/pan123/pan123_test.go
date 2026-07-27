package pan123

import (
	"context"
	"encoding/json"
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

func (store *memoryCacheStore) Get(_ context.Context, key string) (string, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.values[key]
	return value, ok, nil
}

func (store *memoryCacheStore) Set(_ context.Context, key, value string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.values[key] = value
	return nil
}

func (store *memoryCacheStore) SetWithTTL(ctx context.Context, key, value string, _ time.Duration) error {
	return store.Set(ctx, key, value)
}

func TestListAuthenticatesAndFollowsLastFileIDCursor(t *testing.T) {
	var authRequests atomic.Int32
	var listRequests atomic.Int32
	p := New("provider-a", "/", "client-a", "secret-a", "", "", nil)
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case accessTokenPath:
			authRequests.Add(1)
			if req.Method != http.MethodPost {
				return nil, fmt.Errorf("token method = %s", req.Method)
			}
			if got := req.Header.Get("Content-Type"); got != "application/json" {
				return nil, fmt.Errorf("token content-type = %q", got)
			}
			if got := req.Header.Get("Platform"); got != platformHeaderValue {
				return nil, fmt.Errorf("token platform = %q", got)
			}
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				return nil, err
			}
			if body["clientID"] != "client-a" || body["clientSecret"] != "secret-a" {
				return nil, fmt.Errorf("token body = %+v", body)
			}
			return jsonResponse(http.StatusOK, `{
				"code": 0,
				"message": "ok",
				"data": {"accessToken":"access-a","expiredAt":"2099-01-01T00:00:00Z"}
			}`), nil
		case fileListPath:
			listRequests.Add(1)
			if got := req.Header.Get("Authorization"); got != "Bearer access-a" {
				return nil, fmt.Errorf("authorization = %q", got)
			}
			if got := req.Header.Get("Content-Type"); got != "application/json" {
				return nil, fmt.Errorf("content-type = %q", got)
			}
			if got := req.Header.Get("Platform"); got != platformHeaderValue {
				return nil, fmt.Errorf("platform = %q", got)
			}
			if got := req.URL.Query().Get("parentFileId"); got != "0" {
				return nil, fmt.Errorf("parentFileId = %q", got)
			}
			if got := req.URL.Query().Get("limit"); got != "100" {
				return nil, fmt.Errorf("limit = %q", got)
			}
			switch req.URL.Query().Get("lastFileId") {
			case "0":
				return jsonResponse(http.StatusOK, `{
					"code": 0,
					"message": "ok",
					"data": {
						"lastFileId": 10,
						"fileList": [{
							"fileId": 10,
							"filename": "Movies",
							"type": 1,
							"parentFileId": 0,
							"updateAt": "2026-07-26T10:00:00+08:00"
						}, {
							"fileId": 99,
							"filename": "recycled.mkv",
							"type": 0,
							"parentFileId": 0,
							"trashed": 1
						}]
					}
				}`), nil
			case "10":
				return jsonResponse(http.StatusOK, `{
					"code": 0,
					"message": "ok",
					"data": {
						"lastFileId": -1,
						"fileList": [{
							"fileId": "11",
							"filename": "movie.mkv",
							"type": 0,
							"etag": "etag-a",
							"size": "1234",
							"parentFileId": "0"
						}]
					}
				}`), nil
			default:
				return nil, fmt.Errorf("unexpected cursor %q", req.URL.Query().Get("lastFileId"))
			}
		default:
			return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
		}
	})

	items, err := p.List(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	if !items[0].IsDir || items[0].ID != "10" || items[0].Path != "/Movies" {
		t.Fatalf("directory = %+v", items[0])
	}
	if items[1].IsDir || items[1].ID != "11" || items[1].Size != 1234 || items[1].Metadata["etag"] != "etag-a" {
		t.Fatalf("file = %+v", items[1])
	}
	if items[0].ModTime != "2026-07-26T02:00:00Z" {
		t.Fatalf("mod time = %q", items[0].ModTime)
	}
	if authRequests.Load() != 1 || listRequests.Load() != 2 {
		t.Fatalf("requests auth=%d list=%d", authRequests.Load(), listRequests.Load())
	}
}

func TestConfiguredRootTraversesByParentFileID(t *testing.T) {
	var parentIDs []string
	var parentIDsMu sync.Mutex
	p := newTokenProvider("/Movies/Action")
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != fileListPath {
			return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
		}
		parentID := req.URL.Query().Get("parentFileId")
		parentIDsMu.Lock()
		parentIDs = append(parentIDs, parentID)
		parentIDsMu.Unlock()
		switch parentID {
		case "0":
			return fileListResponseJSON("10", "Movies", 1), nil
		case "10":
			return fileListResponseJSON("20", "Action", 1), nil
		case "20":
			return fileListResponseJSON("30", "movie.mkv", 0), nil
		default:
			return nil, fmt.Errorf("unexpected parent id %q", parentID)
		}
	})

	items, err := p.List(context.Background(), "/Movies/Action")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Path != "/Movies/Action/movie.mkv" || items[0].ID != "30" {
		t.Fatalf("items = %+v", items)
	}
	parentIDsMu.Lock()
	defer parentIDsMu.Unlock()
	if strings.Join(parentIDs, ",") != "0,10,20" {
		t.Fatalf("parent ids = %v", parentIDs)
	}
}

func TestListUsesPersistentChildrenCacheAndSupportsBypass(t *testing.T) {
	cache := newMemoryCacheStore()
	var requests atomic.Int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != fileListPath {
			return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
		}
		requests.Add(1)
		return fileListResponseJSON("11", "movie.mkv", 0), nil
	})

	first := newTokenProvider("/", cache)
	first.httpClient.Transport = transport
	items, err := first.List(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}

	second := newTokenProvider("/", cache)
	second.httpClient.Transport = transport
	items, err = second.List(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || requests.Load() != 1 {
		t.Fatalf("cached items=%+v requests=%d", items, requests.Load())
	}
	if _, err := second.List(provideriface.WithBypassCache(context.Background()), "/"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests after bypass = %d", requests.Load())
	}
	if _, ok := cache.values["children:/"]; !ok {
		t.Fatalf("children cache keys = %+v", cache.values)
	}
}

func TestDirectLinkUsesPersistedProviderEntryID(t *testing.T) {
	p := newTokenProvider("/")
	p.LoadPersistedEntryMetadata("/movie.mkv", "12345", map[string]string{
		"parent_id":  "0",
		"entry_type": "file",
		"mime_type":  "video/x-matroska",
	})
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != downloadInfoPath {
			return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
		}
		if req.Method != http.MethodGet || req.URL.Query().Get("fileId") != "12345" {
			return nil, fmt.Errorf("download request = %s %s", req.Method, req.URL.String())
		}
		return jsonResponse(http.StatusOK, `{
			"code": 0,
			"message": "ok",
			"data": {
				"downloadUrl": "https://download.example/movie.mkv",
				"expireAt": "2099-01-01T00:00:00Z"
			}
		}`), nil
	})

	result, err := p.GetDirectLink(context.Background(), "/movie.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if result.URL != "https://download.example/movie.mkv" || result.ExpireAt != "2099-01-01T00:00:00Z" || result.SupportsRange {
		t.Fatalf("result = %+v", result)
	}
}

func TestPersistedEntryIDCannotBypassConfiguredRoot(t *testing.T) {
	p := newTokenProvider("/Movies")
	var requests atomic.Int32
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, fmt.Errorf("unexpected request %s", req.URL)
	})

	_, err := p.GetDirectLinkForEntry(context.Background(), provideriface.DirectLinkInput{
		Path:            "/Outside/movie.mkv",
		ProviderEntryID: "12345",
		Metadata:        map[string]string{"entry_type": "file"},
	})
	if err == nil || !strings.Contains(err.Error(), "outside provider root") {
		t.Fatalf("error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestCachedAncestorCannotBypassConfiguredRoot(t *testing.T) {
	p := newTokenProvider("/Movies/Action")
	p.setCachedNode(node{ID: "10", Path: "/Movies", Name: "Movies", IsDir: true})

	_, err := p.Stat(context.Background(), "/Movies")
	if err == nil || !strings.Contains(err.Error(), "outside provider root") {
		t.Fatalf("error = %v", err)
	}
}

func TestConcurrentUnauthorizedRequestsRenewTokenOnce(t *testing.T) {
	var tokenRequests atomic.Int32
	var callbackRequests atomic.Int32
	p := New(
		"provider-a",
		"/",
		"client-a",
		"secret-a",
		"old-access",
		"2099-01-01T00:00:00Z",
		func(accessToken, expiresAt string) {
			if accessToken != "new-access" || expiresAt != "2099-02-01T00:00:00Z" {
				t.Errorf("renewed token = %q expiry=%q", accessToken, expiresAt)
			}
			callbackRequests.Add(1)
		},
	)
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == accessTokenPath {
			tokenRequests.Add(1)
			return jsonResponse(http.StatusOK, `{
				"code": 0,
				"message": "ok",
				"data": {"accessToken":"new-access","expiredAt":"2099-02-01T00:00:00Z"}
			}`), nil
		}
		if req.Header.Get("Authorization") == "Bearer old-access" {
			return jsonResponse(http.StatusUnauthorized, `{"code":401,"message":"token expired","data":null}`), nil
		}
		return jsonResponse(http.StatusOK, `{
			"code": 0,
			"message": "ok",
			"data": {"lastFileId":-1,"fileList":[]}
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
			_, err := p.listFilesPage(context.Background(), "0", "0", 1)
			errs <- err
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
	if tokenRequests.Load() != 1 || callbackRequests.Load() != 1 {
		t.Fatalf("requests token=%d callback=%d", tokenRequests.Load(), callbackRequests.Load())
	}
}

func TestProviderInstancesShareTokenRefresh(t *testing.T) {
	var tokenRequests atomic.Int32
	var callbackRequests atomic.Int32
	state := NewTokenState("expired-access", "2000-01-01T00:00:00Z")
	onTokenRefreshed := func(accessToken, expiresAt string) {
		if accessToken != "new-access" || expiresAt != "2099-02-01T00:00:00Z" {
			t.Errorf("renewed token = %q expiry=%q", accessToken, expiresAt)
		}
		callbackRequests.Add(1)
	}
	providers := []*Provider{
		NewWithTokenState("provider-a", "/", "client-a", "secret-a", state, onTokenRefreshed),
		NewWithTokenState("provider-a", "/", "client-a", "secret-a", state, onTokenRefreshed),
	}
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == accessTokenPath {
			tokenRequests.Add(1)
			return jsonResponse(http.StatusOK, `{
				"code": 0,
				"message": "ok",
				"data": {"accessToken":"new-access","expiredAt":"2099-02-01T00:00:00Z"}
			}`), nil
		}
		if req.Header.Get("Authorization") != "Bearer new-access" {
			return nil, fmt.Errorf("authorization = %q", req.Header.Get("Authorization"))
		}
		return fileListResponseJSON("", "", 0), nil
	})
	for _, p := range providers {
		p.httpClient.Transport = transport
	}

	start := make(chan struct{})
	errs := make(chan error, len(providers))
	var group sync.WaitGroup
	for _, p := range providers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := p.List(context.Background(), "/")
			errs <- err
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
	if tokenRequests.Load() != 1 || callbackRequests.Load() != 1 {
		t.Fatalf("requests token=%d callback=%d", tokenRequests.Load(), callbackRequests.Load())
	}
}

func TestExpiredTokenRenewsBeforeRequest(t *testing.T) {
	var paths []string
	var pathsMu sync.Mutex
	p := New("provider-a", "/", "client-a", "secret-a", "expired", "2000-01-01T00:00:00Z", nil)
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		pathsMu.Lock()
		paths = append(paths, req.URL.Path)
		pathsMu.Unlock()
		if req.URL.Path == accessTokenPath {
			return jsonResponse(http.StatusOK, `{
				"code": 0,
				"message": "ok",
				"data": {"accessToken":"new-access","expiredAt":"2099-01-01T00:00:00Z"}
			}`), nil
		}
		if req.Header.Get("Authorization") != "Bearer new-access" {
			return nil, fmt.Errorf("authorization = %q", req.Header.Get("Authorization"))
		}
		return fileListResponseJSON("", "", 0), nil
	})

	if _, err := p.List(context.Background(), "/"); err != nil {
		t.Fatal(err)
	}
	pathsMu.Lock()
	defer pathsMu.Unlock()
	if strings.Join(paths, ",") != accessTokenPath+","+fileListPath {
		t.Fatalf("paths = %v", paths)
	}
}

func TestMalformedTokenExpiryRenewsBeforeRequest(t *testing.T) {
	var authRequests atomic.Int32
	p := New("provider-a", "/", "client-a", "secret-a", "stale-access", "not-a-time", nil)
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == accessTokenPath {
			authRequests.Add(1)
			return jsonResponse(http.StatusOK, `{
				"code": 0,
				"message": "ok",
				"data": {"accessToken":"new-access","expiredAt":"2099-01-01T00:00:00Z"}
			}`), nil
		}
		if req.Header.Get("Authorization") != "Bearer new-access" {
			return nil, fmt.Errorf("authorization = %q", req.Header.Get("Authorization"))
		}
		return fileListResponseJSON("", "", 0), nil
	})
	if _, err := p.List(context.Background(), "/"); err != nil {
		t.Fatal(err)
	}
	if authRequests.Load() != 1 {
		t.Fatalf("auth requests = %d", authRequests.Load())
	}
}

func TestRetriesTransientServerFailure(t *testing.T) {
	var requests atomic.Int32
	p := newTokenProvider("/")
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if requests.Add(1) == 1 {
			return jsonResponse(http.StatusServiceUnavailable, `{"code":500,"message":"temporarily unavailable","data":null}`), nil
		}
		return fileListResponseJSON("", "", 0), nil
	})
	if _, err := p.List(context.Background(), "/"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestHTTP200BusinessErrorIsRejected(t *testing.T) {
	p := newTokenProvider("/")
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"code": 5066,
			"message": "file does not exist",
			"data": null
		}`), nil
	})

	_, err := p.List(context.Background(), "/")
	if err == nil {
		t.Fatal("expected a non-zero business code to fail")
	}
	if !strings.Contains(err.Error(), "code=5066") || !strings.Contains(err.Error(), "file does not exist") {
		t.Fatalf("error = %v", err)
	}
}

func TestRetriesRateLimitedBusinessResponse(t *testing.T) {
	var requests atomic.Int32
	p := newTokenProvider("/")
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if requests.Add(1) == 1 {
			return jsonResponse(http.StatusOK, `{"code":429,"message":"too many requests","data":null}`), nil
		}
		return fileListResponseJSON("", "", 0), nil
	})

	if _, err := p.List(context.Background(), "/"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestScanRequestsRespectConfiguredInterval(t *testing.T) {
	const interval = 70 * time.Millisecond
	var requestTimes []time.Time
	var requestTimesMu sync.Mutex
	p := newTokenProvider("/")
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestTimesMu.Lock()
		requestTimes = append(requestTimes, time.Now())
		requestTimesMu.Unlock()
		return fileListResponseJSON("", "", 0), nil
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
		t.Fatalf("request times = %d", len(requestTimes))
	}
	if elapsed := requestTimes[1].Sub(requestTimes[0]); elapsed < 50*time.Millisecond {
		t.Fatalf("request interval = %s", elapsed)
	}
}

func TestCheckStatusUsesConfiguredRoot(t *testing.T) {
	p := newTokenProvider("/Movies")
	var requests atomic.Int32
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests.Add(1)
		switch req.URL.Query().Get("parentFileId") {
		case "0":
			return fileListResponseJSON("10", "Movies", 1), nil
		case "10":
			return fileListResponseJSON("", "", 0), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", req.URL.String())
		}
	})
	status, message := p.CheckStatus(context.Background())
	if status != model.ProviderStatusHealthy || message != "" {
		t.Fatalf("status=%q message=%q", status, message)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func newTokenProvider(rootPath string, cacheStores ...CacheStore) *Provider {
	return New(
		"provider-a",
		rootPath,
		"client-a",
		"secret-a",
		"access-a",
		"2099-01-01T00:00:00Z",
		nil,
		cacheStores...,
	)
}

func fileListResponseJSON(fileID, name string, itemType int) *http.Response {
	fileList := "[]"
	if fileID != "" {
		fileList = fmt.Sprintf(
			`[{"fileId":%q,"filename":%q,"type":%d,"parentFileId":"0"}]`,
			fileID,
			name,
			itemType,
		)
	}
	return jsonResponse(
		http.StatusOK,
		fmt.Sprintf(`{"code":0,"message":"ok","data":{"lastFileId":-1,"fileList":%s}}`, fileList),
	)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

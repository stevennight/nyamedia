package baiduopen

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestListUsesOfficialXPanParametersAndMapsEntries(t *testing.T) {
	p := newTokenProvider("/")
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/rest/2.0/xpan"+fileListPath {
			return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
		}
		query := req.URL.Query()
		for key, want := range map[string]string{
			"method":       "list",
			"dir":          "/",
			"start":        "0",
			"limit":        "1000",
			"access_token": "access-a",
		} {
			if got := query.Get(key); got != want {
				return nil, fmt.Errorf("%s = %q, want %q", key, got, want)
			}
		}
		if got := req.Header.Get("User-Agent"); got != defaultUserAgent {
			return nil, fmt.Errorf("user-agent = %q", got)
		}
		return jsonResponse(http.StatusOK, `{
			"errno": 0,
			"list": [{
				"fs_id": 10,
				"path": "/Movies",
				"server_filename": "Movies",
				"isdir": 1,
				"server_mtime": 1785254400
			}, {
				"fs_id": 11,
				"path": "/Movies/movie.mkv",
				"server_filename": "movie.mkv",
				"isdir": 0,
				"size": 1234,
				"md5": "abc",
				"category": 1
			}]
		}`), nil
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
	if items[1].IsDir || items[1].ID != "11" || items[1].Size != 1234 {
		t.Fatalf("file = %+v", items[1])
	}
	if items[1].Metadata["fs_id"] != "11" || items[1].Metadata["md5"] != "abc" {
		t.Fatalf("metadata = %+v", items[1].Metadata)
	}
}

func TestConfiguredRootResolvesByOfficialDirectoryListing(t *testing.T) {
	p := newTokenProvider("/Movies/Action")
	var dirs []string
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		dir := req.URL.Query().Get("dir")
		dirs = append(dirs, dir)
		switch dir {
		case "/":
			return listResponse("10", "/Movies", "Movies", true), nil
		case "/Movies":
			return listResponse("20", "/Movies/Action", "Action", true), nil
		case "/Movies/Action":
			return listResponse("30", "/Movies/Action/movie.mkv", "movie.mkv", false), nil
		default:
			return nil, fmt.Errorf("unexpected dir %q", dir)
		}
	})

	items, err := p.List(context.Background(), "/Movies/Action")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Path != "/Movies/Action/movie.mkv" {
		t.Fatalf("items = %+v", items)
	}
	if strings.Join(dirs, ",") != "/,/Movies,/Movies/Action" {
		t.Fatalf("dirs = %v", dirs)
	}
}

func TestDirectLinkUsesPersistedFSIDAndRequiredUserAgent(t *testing.T) {
	p := newTokenProvider("/")
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/rest/2.0/xpan"+fileMetaPath {
			return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
		}
		query := req.URL.Query()
		if query.Get("method") != "filemetas" || query.Get("fsids") != "[12345]" || query.Get("dlink") != "1" {
			return nil, fmt.Errorf("query = %s", req.URL.RawQuery)
		}
		return jsonResponse(http.StatusOK, `{
			"errno": 0,
			"list": [{"fs_id":12345,"isdir":0,"dlink":"https://d.pcs.baidu.com/file/movie?fid=12345"}]
		}`), nil
	})

	result, err := p.GetDirectLinkForEntry(context.Background(), provideriface.DirectLinkInput{
		Path:            "/movie.mkv",
		ProviderEntryID: "12345",
		Metadata:        map[string]string{"entry_type": "file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(result.URL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("access_token") != "access-a" {
		t.Fatalf("direct url = %s", result.URL)
	}
	if result.Headers["User-Agent"] != defaultUserAgent || !result.SupportsRange || result.ExpireAt == "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestPersistedFSIDCannotBypassConfiguredRoot(t *testing.T) {
	p := newTokenProvider("/Movies")
	var requests atomic.Int32
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, fmt.Errorf("unexpected request %s", req.URL)
	})

	_, err := p.GetDirectLinkForEntry(context.Background(), provideriface.DirectLinkInput{
		Path:            "/Outside/movie.mkv",
		ProviderEntryID: "12345",
	})
	if err == nil || !strings.Contains(err.Error(), "outside provider root") {
		t.Fatalf("error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestListUsesPersistentChildrenCacheAndSupportsBypass(t *testing.T) {
	cache := newMemoryCacheStore()
	var requests atomic.Int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests.Add(1)
		return listResponse("11", "/movie.mkv", "movie.mkv", false), nil
	})

	first := newTokenProvider("/", cache)
	first.httpClient.Transport = transport
	if _, err := first.List(context.Background(), "/"); err != nil {
		t.Fatal(err)
	}
	second := newTokenProvider("/", cache)
	second.httpClient.Transport = transport
	if _, err := second.List(context.Background(), "/"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("cached requests = %d", requests.Load())
	}
	if _, err := second.List(provideriface.WithBypassCache(context.Background()), "/"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests after bypass = %d", requests.Load())
	}
}

func TestConcurrentExpiredTokenRefreshesOnce(t *testing.T) {
	var tokenRequests atomic.Int32
	var callbackRequests atomic.Int32
	p := New(
		"provider-a",
		"/",
		"client-a",
		"secret-a",
		"expired-access",
		"refresh-a",
		"2000-01-01T00:00:00Z",
		func(accessToken, refreshToken, expiresAt string) {
			if accessToken != "new-access" || refreshToken != "new-refresh" || expiresAt == "" {
				t.Errorf("token callback = %q %q %q", accessToken, refreshToken, expiresAt)
			}
			callbackRequests.Add(1)
		},
	)
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/oauth/2.0/token":
			tokenRequests.Add(1)
			body, _ := io.ReadAll(req.Body)
			values, _ := url.ParseQuery(string(body))
			if values.Get("grant_type") != "refresh_token" ||
				values.Get("client_id") != "client-a" ||
				values.Get("client_secret") != "secret-a" ||
				values.Get("refresh_token") != "refresh-a" {
				return nil, fmt.Errorf("refresh form = %s", body)
			}
			return jsonResponse(http.StatusOK, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":2592000}`), nil
		case "/rest/2.0/xpan/file":
			if req.URL.Query().Get("access_token") != "new-access" {
				return nil, fmt.Errorf("access token = %q", req.URL.Query().Get("access_token"))
			}
			return jsonResponse(http.StatusOK, `{"errno":0,"list":[]}`), nil
		default:
			return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
		}
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
		t.Fatalf("token requests=%d callbacks=%d", tokenRequests.Load(), callbackRequests.Load())
	}
}

func TestFrequencyControlResponseIsRetried(t *testing.T) {
	var requests atomic.Int32
	p := newTokenProvider("/")
	p.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if requests.Add(1) == 1 {
			return jsonResponse(http.StatusOK, `{"errno":31034,"errmsg":"接口请求过于频繁"}`), nil
		}
		return jsonResponse(http.StatusOK, `{"errno":0,"list":[]}`), nil
	})

	if _, err := p.List(context.Background(), "/"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestAuthorizationURLUsesOfficialOAuthScope(t *testing.T) {
	raw := AuthorizationURL("client-a", "https://nya.example/callback", "state-a")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "openapi.baidu.com" || parsed.Path != "/oauth/2.0/authorize" {
		t.Fatalf("url = %s", raw)
	}
	query := parsed.Query()
	if query.Get("response_type") != "code" ||
		query.Get("client_id") != "client-a" ||
		query.Get("redirect_uri") != "https://nya.example/callback" ||
		query.Get("scope") != "basic,netdisk" ||
		query.Get("state") != "state-a" {
		t.Fatalf("query = %v", query)
	}
}

func newTokenProvider(rootPath string, caches ...CacheStore) *Provider {
	return New(
		"provider-a",
		rootPath,
		"client-a",
		"secret-a",
		"access-a",
		"refresh-a",
		"2099-01-01T00:00:00Z",
		nil,
		caches...,
	)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d test", status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func listResponse(id, itemPath, name string, isDir bool) *http.Response {
	dirValue := 0
	if isDir {
		dirValue = 1
	}
	return jsonResponse(http.StatusOK, fmt.Sprintf(`{
		"errno": 0,
		"list": [{
			"fs_id": %s,
			"path": %q,
			"server_filename": %q,
			"isdir": %d
		}]
	}`, id, itemPath, name, dirValue))
}

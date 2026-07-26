package open115

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"NyaMedia/internal/model"
	"NyaMedia/internal/provider"
)

const (
	apiBaseURL     = "https://proapi.115.com"
	authBaseURL    = "https://passportapi.115.com"
	defaultUA      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	pageSize       = 1000
	requestTimeout = 2 * time.Minute

	maxRequestAttempts = 3
	retryBaseDelay     = 250 * time.Millisecond
	childrenCacheTTL   = 10 * time.Minute
)

type CacheStore interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
	SetWithTTL(ctx context.Context, key, value string, ttl time.Duration) error
}

type Provider struct {
	id               string
	rootPath         string
	httpClient       *http.Client
	onTokenRefreshed func(accessToken, refreshToken string)
	cacheStore       CacheStore

	mu           sync.RWMutex
	accessToken  string
	refreshToken string
	cache        map[string]node
	refreshMu    sync.Mutex

	scanRequestMu   sync.Mutex
	lastScanRequest time.Time
}

type node struct {
	ID       string
	ParentID string
	Path     string
	Name     string
	PickCode string
	IsDir    bool
	IsVideo  bool
	Size     int64
	ModTime  string
	MimeType string
}

type childrenCacheEntry struct {
	Items []node `json:"items"`
}

type apiResponse struct {
	State   bool            `json:"state"`
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type authResponse struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Errno   int64           `json:"errno"`
	Data    json.RawMessage `json:"data"`
}

type filesResponse struct {
	State   bool        `json:"state"`
	Code    int64       `json:"code"`
	Message string      `json:"message"`
	Data    []fileItem  `json:"data"`
	Count   int64       `json:"count"`
	Offset  int64       `json:"offset"`
	Limit   json.Number `json:"limit"`
	CID     int64       `json:"cid"`
}

type fileItem struct {
	FID  string `json:"fid"`
	PID  string `json:"pid"`
	FC   string `json:"fc"`
	FN   string `json:"fn"`
	PC   string `json:"pc"`
	Upt  int64  `json:"upt"`
	Uet  int64  `json:"uet"`
	FS   int64  `json:"fs"`
	Ico  string `json:"ico"`
	IsV  int64  `json:"isv"`
	SHA1 string `json:"sha1"`
}

type infoResponse struct {
	Count        int64  `json:"count"`
	Size         string `json:"size"`
	PTime        string `json:"ptime"`
	UTime        string `json:"utime"`
	FileName     string `json:"file_name"`
	PickCode     string `json:"pick_code"`
	FileID       string `json:"file_id"`
	FileCategory string `json:"file_category"`
}

type downloadResponse map[string]struct {
	FileName string `json:"file_name"`
	URL      struct {
		URL string `json:"url"`
	} `json:"url"`
}

type videoPlayResponse struct {
	VideoURL []struct {
		URL        string `json:"url"`
		Definition int    `json:"definition"`
	} `json:"video_url"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func New(id, rootPath, accessToken, refreshToken string, onTokenRefreshed func(accessToken, refreshToken string), cacheStores ...CacheStore) *Provider {
	cleanRoot := normalizePath(rootPath)
	if cleanRoot == "" {
		cleanRoot = "/"
	}

	p := &Provider{
		id:               id,
		rootPath:         cleanRoot,
		httpClient:       &http.Client{Timeout: requestTimeout},
		onTokenRefreshed: onTokenRefreshed,
		accessToken:      strings.TrimSpace(accessToken),
		refreshToken:     strings.TrimSpace(refreshToken),
		cache:            make(map[string]node),
	}
	if len(cacheStores) > 0 {
		p.cacheStore = cacheStores[0]
	}
	if cleanRoot == "/" {
		p.cache["/"] = node{ID: "0", Path: "/", Name: "/", IsDir: true}
	}
	return p

}

func (p *Provider) ID() string {
	return p.id
}

func (p *Provider) Type() string {
	return "115open"
}

func (p *Provider) List(ctx context.Context, providerPath string) ([]provider.Entry, error) {
	dirNode, err := p.resolveDir(ctx, providerPath)
	if err != nil {
		return nil, err
	}

	nodes, err := p.listNodesByID(ctx, dirNode)
	if err != nil {
		return nil, err
	}
	items := make([]provider.Entry, 0, len(nodes))
	for _, item := range nodes {
		items = append(items, toEntry(item))
	}
	return items, nil
}

func (p *Provider) listNodesByID(ctx context.Context, dirNode node) ([]node, error) {
	if items, ok := p.getCachedChildren(ctx, dirNode.Path); ok {
		return items, nil
	}

	items := make([]node, 0)
	offset := int64(0)
	for {
		resp, err := p.getFiles(ctx, dirNode.ID, offset, pageSize)
		if err != nil {
			return nil, err
		}
		for _, item := range resp.Data {
			resolved := p.nodeFromFileItem(dirNode.Path, item)
			p.setCached(resolved)
			items = append(items, resolved)
		}
		if len(resp.Data) < pageSize {
			break
		}
		offset += int64(len(resp.Data))
	}

	p.setCachedChildren(ctx, dirNode.Path, items)
	return items, nil
}

func (p *Provider) Stat(ctx context.Context, providerPath string) (*provider.Entry, error) {
	normalized := normalizePath(providerPath)
	if cached, ok := p.getCached(normalized); ok {
		entry := toEntry(cached)
		return &entry, nil
	}

	if normalized == "/" {
		rootNode, err := p.resolveRoot(ctx)
		if err != nil {
			return nil, err
		}
		entry := toEntry(rootNode)
		return &entry, nil
	}

	info, err := p.getInfoByPath(ctx, p.fullPath(normalized))
	if err != nil {
		return nil, err
	}
	node := p.nodeFromInfo(normalized, info)
	p.setCached(node)
	entry := toEntry(node)
	return &entry, nil
}

func (p *Provider) GetDirectLink(ctx context.Context, providerPath string) (*provider.DirectLinkResult, error) {
	stat, err := p.Stat(ctx, providerPath)
	if err != nil {
		return nil, err
	}
	if stat.IsDir {
		return nil, fmt.Errorf("path %s is a directory", normalizePath(providerPath))
	}

	item, ok := p.getCached(normalizePath(providerPath))
	if !ok || item.PickCode == "" {
		return nil, fmt.Errorf("pick code unavailable for %s", normalizePath(providerPath))
	}

	if item.IsVideo {
		videoURL, err := p.getPlayableURL(ctx, item.PickCode)
		if err == nil && videoURL != "" {
			return p.directLinkResult(ctx, videoURL), nil
		}
	}

	downloadURL, err := p.getDownloadURL(ctx, item.PickCode)
	if err != nil {
		return nil, err
	}
	return p.directLinkResult(ctx, downloadURL), nil
}

func (p *Provider) GetDirectLinkForEntry(ctx context.Context, input provider.DirectLinkInput) (*provider.DirectLinkResult, error) {
	providerPath := normalizePath(input.Path)
	pickCode := strings.TrimSpace(input.Metadata["pick_code"])
	if pickCode == "" {
		p.LoadPersistedEntryMetadata(providerPath, input.ProviderEntryID, input.Metadata)
		return p.GetDirectLink(ctx, providerPath)
	}
	if strings.HasPrefix(strings.TrimSpace(input.Metadata["mime_type"]), "video/") {
		videoURL, err := p.getPlayableURL(ctx, pickCode)
		if err == nil && videoURL != "" {
			return p.directLinkResult(ctx, videoURL), nil
		}
	}
	downloadURL, err := p.getDownloadURL(ctx, pickCode)
	if err != nil {
		return nil, err
	}
	return p.directLinkResult(ctx, downloadURL), nil
}

func (p *Provider) LoadPersistedEntryMetadata(providerPath string, providerEntryID string, metadata map[string]string) {
	normalized := normalizePath(providerPath)
	if normalized == "" || normalized == "/" {
		return
	}
	item := node{
		ID:       strings.TrimSpace(providerEntryID),
		ParentID: strings.TrimSpace(metadata["parent_id"]),
		Path:     normalized,
		Name:     path.Base(normalized),
		PickCode: strings.TrimSpace(metadata["pick_code"]),
		IsDir:    strings.EqualFold(metadata["entry_type"], "dir"),
		Size:     parseInt64(metadata["size"]),
		ModTime:  strings.TrimSpace(metadata["mtime"]),
		MimeType: strings.TrimSpace(metadata["mime_type"]),
	}
	if item.MimeType != "" {
		item.IsVideo = strings.HasPrefix(item.MimeType, "video/")
	}
	if item.ID == "" && item.PickCode == "" {
		return
	}
	p.setCached(item)
}

func (p *Provider) CheckStatus(ctx context.Context) (model.ProviderStatus, string) {
	root, err := p.resolveRoot(ctx)
	if err != nil {
		return model.ProviderStatusError, err.Error()
	}
	if _, err := p.getFiles(ctx, root.ID, 0, 1); err != nil {
		return model.ProviderStatusError, err.Error()
	}
	return model.ProviderStatusHealthy, ""
}

func (p *Provider) WalkFiles(ctx context.Context, sourcePath string, options provider.WalkOptions, fn func(entry provider.Entry) error) error {
	root, err := p.resolveDir(ctx, sourcePath)
	if err != nil {
		return err
	}
	return p.walk(ctx, toEntry(root), options, fn)
}

func (p *Provider) walk(ctx context.Context, current provider.Entry, options provider.WalkOptions, fn func(entry provider.Entry) error) error {
	items, err := p.List(ctx, current.Path)
	if err != nil {
		return err
	}
	if options.BeforeEnterDir != nil {
		decision, err := options.BeforeEnterDir(ctx, current, items)
		if err != nil {
			return err
		}
		if decision == provider.WalkSkipDir {
			return nil
		}
	}
	if err := fn(current); err != nil {
		return err
	}
	for _, item := range items {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if item.IsDir {
			if err := p.walk(ctx, item, options, fn); err != nil {
				return err
			}
			continue
		}
		if err := fn(item); err != nil {
			return err
		}
	}
	return nil
}

func (p *Provider) resolveRoot(ctx context.Context) (node, error) {
	if cached, ok := p.getCached(p.rootPath); ok && cached.ID != "" {
		return cached, nil
	}

	info, err := p.getInfoByPath(ctx, p.rootPath)
	if err != nil {
		return node{}, err
	}
	if p.rootPath != "/" && info.FileID == "" {
		return node{}, fmt.Errorf("115 path not found: %s", p.rootPath)
	}
	rootNode := p.nodeFromInfo(p.rootPath, info)
	if p.rootPath == "/" {
		rootNode.Name = "/"
	}
	p.setCached(rootNode)
	return rootNode, nil
}

func (p *Provider) resolveDir(ctx context.Context, providerPath string) (node, error) {
	item, err := p.resolveNode(ctx, providerPath)
	if err != nil {
		return node{}, err
	}
	if !item.IsDir {
		return node{}, fmt.Errorf("path %s is not a directory", normalizePath(providerPath))
	}
	return item, nil
}

func (p *Provider) resolveNode(ctx context.Context, providerPath string) (node, error) {
	normalized := normalizePath(providerPath)
	if cached, ok := p.getCached(normalized); ok {
		return cached, nil
	}
	if normalized == "/" {
		return p.resolveRoot(ctx)
	}
	if p.rootPath != "/" && !hasPathPrefixFold(normalized, p.rootPath) {
		return node{}, fmt.Errorf("path %s is outside provider root %s", normalized, p.rootPath)
	}
	info, err := p.getInfoByPath(ctx, p.fullPath(normalized))
	if err != nil {
		return node{}, err
	}
	item := p.nodeFromInfo(normalized, info)
	p.setCached(item)
	return item, nil
}

func (p *Provider) getFiles(ctx context.Context, cid string, offset int64, limit int) (*filesResponse, error) {
	values := url.Values{}
	values.Set("cid", cid)
	values.Set("offset", strconv.FormatInt(offset, 10))
	values.Set("limit", strconv.Itoa(limit))
	values.Set("show_dir", "1")

	var resp filesResponse
	if err := p.doRawAPI(ctx, http.MethodGet, apiBaseURL+"/open/ufile/files", values, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (p *Provider) getInfoByPath(ctx context.Context, fullPath string) (*infoResponse, error) {
	form := map[string]string{"path": fullPath}
	var resp infoResponse
	if err := p.doAPI(ctx, http.MethodPost, apiBaseURL+"/open/folder/get_info", nil, form, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (p *Provider) getDownloadURL(ctx context.Context, pickCode string) (string, error) {
	var resp downloadResponse
	if err := p.doAPI(ctx, http.MethodPost, apiBaseURL+"/open/ufile/downurl", nil, map[string]string{"pick_code": pickCode}, &resp); err != nil {
		return "", err
	}
	for _, item := range resp {
		if item.URL.URL != "" {
			return item.URL.URL, nil
		}
	}
	return "", fmt.Errorf("download url unavailable")
}

func (p *Provider) getPlayableURL(ctx context.Context, pickCode string) (string, error) {
	values := url.Values{}
	values.Set("pick_code", pickCode)
	var resp videoPlayResponse
	if err := p.doAPI(ctx, http.MethodGet, apiBaseURL+"/open/video/play", values, nil, &resp); err != nil {
		return "", err
	}
	bestURL := ""
	bestDef := -1
	for _, item := range resp.VideoURL {
		if item.URL == "" {
			continue
		}
		if item.Definition > bestDef {
			bestDef = item.Definition
			bestURL = item.URL
		}
	}
	if bestURL == "" {
		return "", fmt.Errorf("play url unavailable")
	}
	return bestURL, nil
}

func (p *Provider) directLinkResult(ctx context.Context, directURL string) *provider.DirectLinkResult {
	return &provider.DirectLinkResult{
		URL: directURL,
		Headers: map[string]string{
			"User-Agent": requestUserAgent(ctx),
		},
		SupportsRange: true,
	}
}

func (p *Provider) doAPI(ctx context.Context, method, endpoint string, query url.Values, form map[string]string, out any) error {
	return p.doAPIWithRetry(ctx, method, endpoint, query, form, out, false, 0)
}

func (p *Provider) doRawAPI(ctx context.Context, method, endpoint string, query url.Values, form map[string]string, out any) error {
	return p.doRawAPIWithRetry(ctx, method, endpoint, query, form, out, false, 0)
}

func (p *Provider) doAPIWithRetry(ctx context.Context, method, endpoint string, query url.Values, form map[string]string, out any, authRetried bool, attempt int) error {
	return p.doAPIRequest(ctx, method, endpoint, query, form, out, false, authRetried, attempt)
}

func (p *Provider) doRawAPIWithRetry(ctx context.Context, method, endpoint string, query url.Values, form map[string]string, out any, authRetried bool, attempt int) error {
	return p.doAPIRequest(ctx, method, endpoint, query, form, out, true, authRetried, attempt)
}

func (p *Provider) doAPIRequest(ctx context.Context, method, endpoint string, query url.Values, form map[string]string, out any, raw, authRetried bool, attempt int) error {
	accessToken := p.accessTokenValue()
	if accessToken == "" && p.refreshTokenValue() != "" {
		if err := p.refreshAccessToken(ctx, accessToken); err != nil {
			return err
		}
		accessToken = p.accessTokenValue()
	}

	body, contentType, err := buildMultipartForm(form)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if query != nil {
		req.URL.RawQuery = query.Encode()
	}
	req.Header.Set("User-Agent", requestUserAgent(ctx))
	if form != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	if err := p.waitScanRequest(ctx); err != nil {
		return err
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		if attempt+1 < maxRequestAttempts && isRetryableTransportError(err) {
			if waitErr := waitRetry(ctx, attempt); waitErr != nil {
				return waitErr
			}
			return p.doAPIRequest(ctx, method, endpoint, query, form, out, raw, authRetried, attempt+1)
		}
		return err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var meta apiResponse
	if err := json.Unmarshal(responseBody, &meta); err != nil {
		if attempt+1 < maxRequestAttempts && isRetryableHTTPStatus(resp.StatusCode) {
			if waitErr := waitRetry(ctx, attempt); waitErr != nil {
				return waitErr
			}
			return p.doAPIRequest(ctx, method, endpoint, query, form, out, raw, authRetried, attempt+1)
		}
		return fmt.Errorf("decode 115open response status=%d: %w", resp.StatusCode, err)
	}
	if !meta.State {
		if !authRetried && shouldRefresh(meta.Code, resp.StatusCode) && p.refreshTokenValue() != "" {
			if err := p.refreshAccessToken(ctx, accessToken); err != nil {
				return err
			}
			return p.doAPIRequest(ctx, method, endpoint, query, form, out, raw, true, 0)
		}
		if attempt+1 < maxRequestAttempts && isRetryableAPIResponse(resp.StatusCode, meta.Code, meta.Message) {
			if waitErr := waitRetry(ctx, attempt); waitErr != nil {
				return waitErr
			}
			return p.doAPIRequest(ctx, method, endpoint, query, form, out, raw, authRetried, attempt+1)
		}
		return fmt.Errorf("115open api error status=%d code=%d message=%s", resp.StatusCode, meta.Code, meta.Message)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("115open api unexpected status=%d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if raw {
		return json.Unmarshal(responseBody, out)
	}
	if len(meta.Data) == 0 || string(meta.Data) == "null" {
		return nil
	}
	return json.Unmarshal(meta.Data, out)
}

func (p *Provider) waitScanRequest(ctx context.Context) error {
	interval := provider.ScanRequestIntervalFromContext(ctx)
	if interval <= 0 {
		return nil
	}

	p.scanRequestMu.Lock()
	defer p.scanRequestMu.Unlock()
	if !p.lastScanRequest.IsZero() {
		waitFor := p.lastScanRequest.Add(interval).Sub(time.Now())
		if waitFor > 0 {
			timer := time.NewTimer(waitFor)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	p.lastScanRequest = time.Now()
	return nil
}

func (p *Provider) refreshAccessToken(ctx context.Context, staleAccessToken string) error {
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()

	currentAccessToken := p.accessTokenValue()
	if currentAccessToken != "" && currentAccessToken != staleAccessToken {
		return nil
	}

	refreshToken := p.refreshTokenValue()
	if refreshToken == "" {
		return fmt.Errorf("115open refresh_token is required")
	}

	body, contentType, err := buildMultipartForm(map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authBaseURL+"/open/refreshToken", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", requestUserAgent(ctx))

	for attempt := 0; attempt < maxRequestAttempts; attempt++ {
		resp, err := p.httpClient.Do(req)
		if err != nil {
			if attempt+1 < maxRequestAttempts && isRetryableTransportError(err) {
				if waitErr := waitRetry(ctx, attempt); waitErr != nil {
					return waitErr
				}
				req, err = http.NewRequestWithContext(ctx, http.MethodPost, authBaseURL+"/open/refreshToken", bytes.NewReader(body))
				if err != nil {
					return err
				}
				req.Header.Set("Content-Type", contentType)
				req.Header.Set("User-Agent", requestUserAgent(ctx))
				continue
			}
			return err
		}

		responseBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}

		var envelope authResponse
		if err := json.Unmarshal(responseBody, &envelope); err != nil {
			if attempt+1 < maxRequestAttempts && isRetryableHTTPStatus(resp.StatusCode) {
				if waitErr := waitRetry(ctx, attempt); waitErr != nil {
					return waitErr
				}
				req, err = http.NewRequestWithContext(ctx, http.MethodPost, authBaseURL+"/open/refreshToken", bytes.NewReader(body))
				if err != nil {
					return err
				}
				req.Header.Set("Content-Type", contentType)
				req.Header.Set("User-Agent", requestUserAgent(ctx))
				continue
			}
			return fmt.Errorf("decode 115open refresh response status=%d: %w", resp.StatusCode, err)
		}
		if attempt+1 < maxRequestAttempts && isRetryableHTTPStatus(resp.StatusCode) {
			if waitErr := waitRetry(ctx, attempt); waitErr != nil {
				return waitErr
			}
			req, err = http.NewRequestWithContext(ctx, http.MethodPost, authBaseURL+"/open/refreshToken", bytes.NewReader(body))
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("User-Agent", requestUserAgent(ctx))
			continue
		}
		if envelope.Code != 0 {
			if envelope.Error != "" {
				return fmt.Errorf("115open auth error status=%d code=%d errno=%d error=%s", resp.StatusCode, envelope.Code, envelope.Errno, envelope.Error)
			}
			return fmt.Errorf("115open auth error status=%d code=%d message=%s", resp.StatusCode, envelope.Code, envelope.Message)
		}

		var token tokenResponse
		if err := json.Unmarshal(envelope.Data, &token); err != nil {
			return err
		}
		if strings.TrimSpace(token.AccessToken) == "" {
			return fmt.Errorf("115open refresh response missing access_token")
		}
		p.setTokens(token.AccessToken, token.RefreshToken)
		return nil
	}

	return fmt.Errorf("115open refresh token failed after %d attempts", maxRequestAttempts)
}

func (p *Provider) setTokens(accessToken, refreshToken string) {
	p.mu.Lock()
	p.accessToken = strings.TrimSpace(accessToken)
	if strings.TrimSpace(refreshToken) != "" {
		p.refreshToken = strings.TrimSpace(refreshToken)
	}
	updatedAccessToken := p.accessToken
	updatedRefreshToken := p.refreshToken
	onTokenRefreshed := p.onTokenRefreshed
	p.mu.Unlock()

	if onTokenRefreshed != nil {
		onTokenRefreshed(updatedAccessToken, updatedRefreshToken)
	}
}

func (p *Provider) accessTokenValue() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.accessToken
}

func (p *Provider) refreshTokenValue() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.refreshToken
}

func (p *Provider) getCached(providerPath string) (node, bool) {
	normalized := normalizePath(providerPath)
	p.mu.RLock()
	item, ok := p.cache[normalized]
	p.mu.RUnlock()
	if ok {
		return item, true
	}
	return p.getPersistentCachedNode(normalized)
}

func (p *Provider) setCached(item node) {
	if item.Path == "" {
		return
	}
	p.mu.Lock()
	p.cache[normalizePath(item.Path)] = item
	p.mu.Unlock()
	if item.IsDir {
		p.setPersistentCache("node:"+normalizePath(item.Path), item)
	}
}

func (p *Provider) getPersistentCachedNode(providerPath string) (node, bool) {
	if p.cacheStore == nil {
		return node{}, false
	}
	value, ok, err := p.cacheStore.Get(context.Background(), "node:"+normalizePath(providerPath))
	if err != nil || !ok {
		return node{}, false
	}
	var item node
	if err := json.Unmarshal([]byte(value), &item); err != nil || item.Path == "" {
		return node{}, false
	}
	p.mu.Lock()
	p.cache[normalizePath(item.Path)] = item
	p.mu.Unlock()
	return item, true
}

func (p *Provider) getCachedChildren(ctx context.Context, providerPath string) ([]node, bool) {
	if provider.BypassCacheFromContext(ctx) || p.cacheStore == nil {
		return nil, false
	}
	value, ok, err := p.cacheStore.Get(ctx, "children:"+normalizePath(providerPath))
	if err != nil || !ok {
		return nil, false
	}
	var cached childrenCacheEntry
	if err := json.Unmarshal([]byte(value), &cached); err != nil {
		return nil, false
	}
	p.mu.Lock()
	for _, item := range cached.Items {
		p.cache[normalizePath(item.Path)] = item
	}
	p.mu.Unlock()
	return cached.Items, true
}

func (p *Provider) setCachedChildren(ctx context.Context, providerPath string, items []node) {
	if p.cacheStore == nil {
		return
	}
	encoded, err := json.Marshal(childrenCacheEntry{Items: items})
	if err != nil {
		return
	}
	_ = p.cacheStore.SetWithTTL(ctx, "children:"+normalizePath(providerPath), string(encoded), childrenCacheTTL)
}

func (p *Provider) setPersistentCache(key string, value any) {
	if p.cacheStore == nil {
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	_ = p.cacheStore.Set(context.Background(), key, string(encoded))
}

func (p *Provider) nodeFromInfo(providerPath string, info *infoResponse) node {
	item := node{
		ID:       info.FileID,
		Path:     normalizePath(providerPath),
		Name:     info.FileName,
		PickCode: info.PickCode,
		IsDir:    info.FileCategory == "0",
		Size:     parseInt64(info.Size),
		ModTime:  parseTimestamp(info.UTime, info.PTime),
	}
	if !item.IsDir {
		item.MimeType = detectMimeType(info.FileName)
		item.IsVideo = strings.HasPrefix(item.MimeType, "video/")
	}
	return item
}

func (p *Provider) nodeFromFileItem(parentPath string, item fileItem) node {
	childPath := normalizePath(path.Join(normalizePath(parentPath), item.FN))
	nodeItem := node{
		ID:       item.FID,
		ParentID: item.PID,
		Path:     childPath,
		Name:     item.FN,
		PickCode: item.PC,
		IsDir:    item.FC == "0",
		IsVideo:  item.IsV == 1,
		Size:     item.FS,
		ModTime:  formatUnixTime(item.Uet, item.Upt),
	}
	if !nodeItem.IsDir {
		nodeItem.MimeType = detectMimeType(item.FN)
	}
	return nodeItem
}

func toEntry(item node) provider.Entry {
	entry := provider.Entry{
		ID:       item.ID,
		Name:     item.Name,
		Path:     item.Path,
		IsDir:    item.IsDir,
		Size:     item.Size,
		ModTime:  item.ModTime,
		MimeType: item.MimeType,
	}
	metadata := map[string]string{"entry_type": "file"}
	if item.IsDir {
		metadata["entry_type"] = "dir"
	}
	if item.ParentID != "" {
		metadata["parent_id"] = item.ParentID
	}
	if item.PickCode != "" {
		metadata["pick_code"] = item.PickCode
	}
	if item.MimeType != "" {
		metadata["mime_type"] = item.MimeType
	}
	if item.ModTime != "" {
		metadata["mtime"] = item.ModTime
	}
	if item.Size > 0 {
		metadata["size"] = strconv.FormatInt(item.Size, 10)
	}
	entry.Metadata = metadata
	return entry
}

func (p *Provider) fullPath(providerPath string) string {
	normalized := normalizePath(providerPath)
	if p.rootPath == "/" {
		return normalized
	}
	if hasPathPrefixFold(normalized, p.rootPath) {
		return normalized
	}
	if normalized == "/" {
		return p.rootPath
	}
	return normalizePath(path.Join(p.rootPath, normalized))
}

func hasPathPrefixFold(value, prefix string) bool {
	valueParts := splitPathSegments(value)
	prefixParts := splitPathSegments(prefix)
	if len(prefixParts) == 0 {
		return true
	}
	if len(valueParts) < len(prefixParts) {
		return false
	}
	for i := range prefixParts {
		if !strings.EqualFold(valueParts[i], prefixParts[i]) {
			return false
		}
	}
	return true
}

func splitPathSegments(value string) []string {
	trimmed := strings.Trim(normalizePath(value), "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func normalizePath(value string) string {
	clean := path.Clean("/" + strings.TrimSpace(value))
	if clean == "." {
		return "/"
	}
	return clean
}

func formatUnixTime(primary, fallback int64) string {
	if primary > 0 {
		return time.Unix(primary, 0).UTC().Format(time.RFC3339)
	}
	if fallback > 0 {
		return time.Unix(fallback, 0).UTC().Format(time.RFC3339)
	}
	return ""
}

func parseTimestamp(values ...string) string {
	for _, value := range values {
		if unixValue, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && unixValue > 0 {
			return time.Unix(unixValue, 0).UTC().Format(time.RFC3339)
		}
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func detectMimeType(name string) string {
	return mime.TypeByExtension(strings.ToLower(path.Ext(name)))
}

func shouldRefresh(code int64, statusCode int) bool {
	if statusCode == http.StatusUnauthorized {
		return true
	}
	if code == 99 || code == 401 {
		return true
	}
	return code >= 40100 && code < 40200
}

func requestUserAgent(ctx context.Context) string {
	if userAgent := provider.RequestUserAgentFromContext(ctx); userAgent != "" {
		return userAgent
	}
	return defaultUA
}

func isRetryableTransportError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"connection reset", "connection refused", "connection aborted", "server closed idle connection", "unexpected eof", "eof"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isRetryableHTTPStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}

func isRetryableAPIResponse(statusCode int, code int64, message string) bool {
	if isRetryableHTTPStatus(statusCode) || code == http.StatusTooManyRequests {
		return true
	}
	message = strings.ToLower(strings.TrimSpace(message))
	for _, marker := range []string{"频繁", "稍后", "繁忙", "rate limit", "too many", "temporarily unavailable", "timeout"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func waitRetry(ctx context.Context, attempt int) error {
	delay := retryBaseDelay * time.Duration(1<<attempt)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func buildMultipartForm(form map[string]string) ([]byte, string, error) {
	if form == nil {
		return nil, "", nil
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range form {
		if err := writer.WriteField(key, value); err != nil {
			return nil, "", err
		}
	}
	contentType := writer.FormDataContentType()
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), contentType, nil
}

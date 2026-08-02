package baiduopen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
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
	apiBaseURL        = "https://pan.baidu.com/rest/2.0/xpan"
	oauthAuthorizeURL = "https://openapi.baidu.com/oauth/2.0/authorize"
	oauthTokenURL     = "https://openapi.baidu.com/oauth/2.0/token"

	fileListPath = "/file"
	fileMetaPath = "/multimedia"

	defaultUserAgent = "pan.baidu.com"
	pageSize         = 1000
	requestTimeout   = 2 * time.Minute

	maxRequestAttempts = 3
	retryBaseDelay     = 500 * time.Millisecond
	childrenCacheTTL   = 10 * time.Minute
	tokenExpiryLeeway  = time.Minute
	directLinkTTL      = 7 * time.Hour
)

type CacheStore interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
	SetWithTTL(ctx context.Context, key, value string, ttl time.Duration) error
}

type TokenState struct {
	mu           sync.Mutex
	accessToken  string
	refreshToken string
	expiresAt    string
}

func NewTokenState(accessToken, refreshToken, expiresAt string) *TokenState {
	return &TokenState{
		accessToken:  strings.TrimSpace(accessToken),
		refreshToken: strings.TrimSpace(refreshToken),
		expiresAt:    strings.TrimSpace(expiresAt),
	}
}

type OAuthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

type Provider struct {
	id               string
	rootPath         string
	clientID         string
	clientSecret     string
	httpClient       *http.Client
	onTokenRefreshed func(accessToken, refreshToken, expiresAt string)
	cacheStore       CacheStore
	tokenState       *TokenState

	mu          sync.RWMutex
	nodesByPath map[string]node

	scanRequestMu   sync.Mutex
	lastScanRequest time.Time
}

type node struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	MD5      string `json:"md5,omitempty"`
	IsDir    bool   `json:"is_dir"`
	Size     int64  `json:"size,omitempty"`
	ModTime  string `json:"mod_time,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Category int    `json:"category,omitempty"`
}

type childrenCacheEntry struct {
	Items []node `json:"items"`
}

type apiResponse struct {
	Errno     int64  `json:"errno"`
	ErrMsg    string `json:"errmsg"`
	ErrorCode int64  `json:"error_code"`
	ErrorMsg  string `json:"error_msg"`
}

type fileListResponse struct {
	apiResponse
	List []fileItem `json:"list"`
}

type fileMetaResponse struct {
	apiResponse
	List []fileMeta `json:"list"`
}

type fileItem struct {
	FSID           json.Number `json:"fs_id"`
	Path           string      `json:"path"`
	ServerFilename string      `json:"server_filename"`
	Size           json.Number `json:"size"`
	IsDir          int         `json:"isdir"`
	ServerMTime    int64       `json:"server_mtime"`
	LocalMTime     int64       `json:"local_mtime"`
	MD5            string      `json:"md5"`
	Category       int         `json:"category"`
}

type fileMeta struct {
	FSID  json.Number `json:"fs_id"`
	DLink string      `json:"dlink"`
	IsDir int         `json:"isdir"`
}

type oauthResponse struct {
	OAuthToken
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type apiError struct {
	StatusCode int
	Code       int64
	Message    string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("baiduopen api error status=%d code=%d message=%s", e.StatusCode, e.Code, e.Message)
}

func New(
	id,
	rootPath,
	clientID,
	clientSecret,
	accessToken,
	refreshToken,
	accessTokenExpiresAt string,
	onTokenRefreshed func(accessToken, refreshToken, expiresAt string),
	cacheStores ...CacheStore,
) *Provider {
	return NewWithTokenState(
		id,
		rootPath,
		clientID,
		clientSecret,
		NewTokenState(accessToken, refreshToken, accessTokenExpiresAt),
		onTokenRefreshed,
		cacheStores...,
	)
}

func NewWithTokenState(
	id,
	rootPath,
	clientID,
	clientSecret string,
	tokenState *TokenState,
	onTokenRefreshed func(accessToken, refreshToken, expiresAt string),
	cacheStores ...CacheStore,
) *Provider {
	cleanRoot := normalizePath(rootPath)
	if tokenState == nil {
		tokenState = NewTokenState("", "", "")
	}
	p := &Provider{
		id:               id,
		rootPath:         cleanRoot,
		clientID:         strings.TrimSpace(clientID),
		clientSecret:     strings.TrimSpace(clientSecret),
		httpClient:       &http.Client{Timeout: requestTimeout},
		onTokenRefreshed: onTokenRefreshed,
		tokenState:       tokenState,
		nodesByPath:      make(map[string]node),
	}
	if len(cacheStores) > 0 {
		p.cacheStore = cacheStores[0]
	}
	if cleanRoot == "/" {
		p.nodesByPath["/"] = node{ID: "0", Path: "/", Name: "/", IsDir: true}
	}
	return p
}

func (p *Provider) ID() string {
	return p.id
}

func (p *Provider) Type() string {
	return "baiduopen"
}

func (p *Provider) List(ctx context.Context, providerPath string) ([]provider.Entry, error) {
	parent, err := p.resolveDir(ctx, providerPath)
	if err != nil {
		return nil, err
	}
	items, err := p.listNodes(ctx, parent)
	if err != nil {
		return nil, err
	}
	entries := make([]provider.Entry, 0, len(items))
	for _, item := range items {
		entries = append(entries, toEntry(item))
	}
	return entries, nil
}

func (p *Provider) Stat(ctx context.Context, providerPath string) (*provider.Entry, error) {
	item, err := p.resolveNode(ctx, providerPath)
	if err != nil {
		return nil, err
	}
	entry := toEntry(item)
	return &entry, nil
}

func (p *Provider) GetDirectLinkForEntry(ctx context.Context, input provider.DirectLinkInput) (*provider.DirectLinkResult, error) {
	providerPath := normalizePath(input.Path)
	if err := p.validatePathWithinRoot(providerPath); err != nil {
		return nil, err
	}
	fileID := strings.TrimSpace(input.ProviderEntryID)
	if fileID == "" {
		fileID = strings.TrimSpace(input.Metadata["fs_id"])
	}
	if fileID == "" {
		item, err := p.resolveNode(ctx, providerPath)
		if err != nil {
			return nil, err
		}
		if item.IsDir {
			return nil, fmt.Errorf("path %s is a directory", providerPath)
		}
		fileID = item.ID
	}
	return p.directLinkForFileID(ctx, providerPath, fileID)
}

func (p *Provider) directLinkForFileID(ctx context.Context, providerPath, fileID string) (*provider.DirectLinkResult, error) {
	if _, err := strconv.ParseUint(fileID, 10, 64); err != nil {
		return nil, fmt.Errorf("invalid baidu fs_id for %s", providerPath)
	}
	meta, err := p.getFileMeta(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if meta.IsDir != 0 {
		return nil, fmt.Errorf("path %s is a directory", providerPath)
	}
	directURL := strings.TrimSpace(meta.DLink)
	if directURL == "" {
		return nil, fmt.Errorf("baiduopen download url unavailable for %s", providerPath)
	}
	accessToken, _, _ := p.tokenState.snapshot()
	directURL, err = appendAccessToken(directURL, accessToken)
	if err != nil {
		return nil, err
	}
	return &provider.DirectLinkResult{
		URL: directURL,
		Headers: map[string]string{
			"User-Agent": defaultUserAgent,
		},
		ExpireAt:      time.Now().UTC().Add(directLinkTTL).Format(time.RFC3339),
		SupportsRange: true,
	}, nil
}

func (p *Provider) LoadPersistedEntryMetadata(providerPath, providerEntryID string, metadata map[string]string) {
	normalized := normalizePath(providerPath)
	if normalized == "/" || p.validatePathWithinRoot(normalized) != nil {
		return
	}
	item := node{
		ID:       strings.TrimSpace(providerEntryID),
		ParentID: strings.TrimSpace(metadata["parent_id"]),
		Path:     normalized,
		Name:     path.Base(normalized),
		MD5:      strings.TrimSpace(metadata["md5"]),
		IsDir:    strings.EqualFold(metadata["entry_type"], "dir"),
		Size:     parseInt64(metadata["size"]),
		ModTime:  strings.TrimSpace(metadata["mtime"]),
		MimeType: strings.TrimSpace(metadata["mime_type"]),
		Category: int(parseInt64(metadata["category"])),
	}
	if item.ID == "" {
		item.ID = strings.TrimSpace(metadata["fs_id"])
	}
	if item.ID != "" {
		p.setCachedNode(item)
	}
}

func (p *Provider) CheckStatus(ctx context.Context) (model.ProviderStatus, string) {
	root, err := p.resolveRoot(ctx)
	if err != nil {
		return model.ProviderStatusError, err.Error()
	}
	if _, err := p.listFilesPage(ctx, root.Path, 0, 1); err != nil {
		return model.ProviderStatusError, err.Error()
	}
	return model.ProviderStatusHealthy, ""
}

func (p *Provider) WalkFiles(ctx context.Context, sourcePath string, options provider.WalkOptions, fn func(entry provider.Entry) error) error {
	root, err := p.resolveDir(ctx, sourcePath)
	if err != nil {
		return err
	}
	return p.walk(ctx, root, options, fn)
}

func (p *Provider) walk(ctx context.Context, current node, options provider.WalkOptions, fn func(entry provider.Entry) error) error {
	items, err := p.listNodes(ctx, current)
	if err != nil {
		return err
	}
	children := make([]provider.Entry, 0, len(items))
	for _, item := range items {
		children = append(children, toEntry(item))
	}
	if options.BeforeEnterDir != nil {
		decision, err := options.BeforeEnterDir(ctx, toEntry(current), children)
		if err != nil {
			return err
		}
		if decision == provider.WalkSkipDir {
			return nil
		}
	}
	if err := fn(toEntry(current)); err != nil {
		return err
	}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if item.IsDir {
			if err := p.walk(ctx, item, options, fn); err != nil {
				return err
			}
			continue
		}
		if err := fn(toEntry(item)); err != nil {
			return err
		}
	}
	return nil
}

func (p *Provider) listNodes(ctx context.Context, parent node) ([]node, error) {
	if items, ok := p.getCachedChildren(ctx, parent.Path); ok {
		return items, nil
	}
	files, err := p.listFiles(ctx, parent.Path)
	if err != nil {
		return nil, err
	}
	items := make([]node, 0, len(files))
	for _, file := range files {
		item := nodeFromFileItem(parent.Path, file)
		if item.ID == "" || item.Name == "" {
			continue
		}
		p.setCachedNode(item)
		items = append(items, item)
	}
	p.setCachedChildren(ctx, parent.Path, items)
	return items, nil
}

func (p *Provider) listFiles(ctx context.Context, dir string) ([]fileItem, error) {
	items := make([]fileItem, 0, pageSize)
	start := 0
	for {
		page, err := p.listFilesPage(ctx, dir, start, pageSize)
		if err != nil {
			return nil, err
		}
		items = append(items, page.List...)
		if len(page.List) < pageSize {
			break
		}
		start += len(page.List)
	}
	return items, nil
}

func (p *Provider) listFilesPage(ctx context.Context, dir string, start, limit int) (*fileListResponse, error) {
	query := url.Values{}
	query.Set("method", "list")
	query.Set("dir", normalizePath(dir))
	query.Set("start", strconv.Itoa(start))
	query.Set("limit", strconv.Itoa(limit))
	query.Set("order", "name")
	query.Set("desc", "0")
	query.Set("web", "1")
	var result fileListResponse
	if err := p.doAPI(ctx, http.MethodGet, fileListPath, query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (p *Provider) getFileMeta(ctx context.Context, fileID string) (*fileMeta, error) {
	query := url.Values{}
	query.Set("method", "filemetas")
	query.Set("fsids", "["+fileID+"]")
	query.Set("dlink", "1")
	var result fileMetaResponse
	if err := p.doAPI(ctx, http.MethodGet, fileMetaPath, query, &result); err != nil {
		return nil, err
	}
	if len(result.List) == 0 {
		return nil, fmt.Errorf("baiduopen file metadata unavailable")
	}
	return &result.List[0], nil
}

func (p *Provider) resolveNode(ctx context.Context, providerPath string) (node, error) {
	normalized := normalizePath(providerPath)
	if err := p.validatePathWithinRoot(normalized); err != nil {
		return node{}, err
	}
	if cached, ok := p.getCachedNode(normalized); ok {
		return cached, nil
	}
	if normalized == p.rootPath {
		return p.resolveRoot(ctx)
	}
	if normalized == "/" {
		return p.resolveRoot(ctx)
	}
	parentPath := normalizePath(path.Dir(normalized))
	parent, err := p.resolveDir(ctx, parentPath)
	if err != nil {
		return node{}, err
	}
	return p.findNamedChild(ctx, parent, path.Base(normalized))
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

func (p *Provider) resolveRoot(ctx context.Context) (node, error) {
	if cached, ok := p.getCachedNode(p.rootPath); ok && cached.ID != "" {
		return cached, nil
	}
	if p.rootPath == "/" {
		root := node{ID: "0", Path: "/", Name: "/", IsDir: true}
		p.setCachedNode(root)
		return root, nil
	}
	parentPath := normalizePath(path.Dir(p.rootPath))
	parent := node{ID: "0", Path: "/", Name: "/", IsDir: true}
	if parentPath != "/" {
		var err error
		parent, err = p.resolveAbsoluteDir(ctx, parentPath)
		if err != nil {
			return node{}, fmt.Errorf("baiduopen root path not found: %s: %w", p.rootPath, err)
		}
	}
	item, err := p.findNamedChild(ctx, parent, path.Base(p.rootPath))
	if err != nil {
		return node{}, fmt.Errorf("baiduopen root path not found: %s: %w", p.rootPath, err)
	}
	if !item.IsDir {
		return node{}, fmt.Errorf("baiduopen root path is not a directory: %s", p.rootPath)
	}
	p.setCachedNode(item)
	return item, nil
}

func (p *Provider) resolveAbsoluteDir(ctx context.Context, dirPath string) (node, error) {
	current := node{ID: "0", Path: "/", Name: "/", IsDir: true}
	for _, segment := range splitPathSegments(dirPath) {
		child, err := p.findNamedChild(ctx, current, segment)
		if err != nil {
			return node{}, err
		}
		if !child.IsDir {
			return node{}, fmt.Errorf("path %s is not a directory", child.Path)
		}
		current = child
	}
	return current, nil
}

func (p *Provider) findNamedChild(ctx context.Context, parent node, name string) (node, error) {
	childPath := normalizePath(path.Join(parent.Path, name))
	if cached, ok := p.getCachedNode(childPath); ok {
		return cached, nil
	}
	items, err := p.listNodes(ctx, parent)
	if err != nil {
		return node{}, err
	}
	for _, item := range items {
		if item.Name == name {
			return item, nil
		}
	}
	return node{}, fmt.Errorf("baiduopen path not found: %s", childPath)
}

func (p *Provider) validatePathWithinRoot(providerPath string) error {
	normalized := normalizePath(providerPath)
	if p.rootPath != "/" && !hasPathPrefix(normalized, p.rootPath) {
		return fmt.Errorf("path %s is outside provider root %s", normalized, p.rootPath)
	}
	return nil
}

func (p *Provider) doAPI(ctx context.Context, method, endpointPath string, query url.Values, out any) error {
	return p.doAPIWithRetry(ctx, method, endpointPath, query, out, false, 0)
}

func (p *Provider) doAPIWithRetry(
	ctx context.Context,
	method,
	endpointPath string,
	query url.Values,
	out any,
	authRetried bool,
	attempt int,
) error {
	accessToken, err := p.ensureAccessToken(ctx)
	if err != nil {
		return err
	}
	requestQuery := cloneValues(query)
	requestQuery.Set("access_token", accessToken)
	req, err := http.NewRequestWithContext(ctx, method, apiBaseURL+endpointPath+"?"+requestQuery.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	if err := p.waitScanRequest(ctx); err != nil {
		return err
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		if attempt+1 < maxRequestAttempts && isRetryableTransportError(err) {
			if waitErr := waitRetry(ctx, attempt); waitErr != nil {
				return waitErr
			}
			return p.doAPIWithRetry(ctx, method, endpointPath, query, out, authRetried, attempt+1)
		}
		return fmt.Errorf("baiduopen request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read baiduopen response: %w", err)
	}

	var envelope apiResponse
	decodeErr := json.Unmarshal(body, &envelope)
	code := envelope.code()
	message := envelope.message()
	if !authRetried && shouldRefreshToken(resp.StatusCode, code) {
		if err := p.refreshAccessToken(ctx, accessToken, true); err != nil {
			return err
		}
		return p.doAPIWithRetry(ctx, method, endpointPath, query, out, true, 0)
	}
	if attempt+1 < maxRequestAttempts && isRetryableAPIResponse(resp.StatusCode, code, message) {
		if waitErr := waitRetry(ctx, attempt); waitErr != nil {
			return waitErr
		}
		return p.doAPIWithRetry(ctx, method, endpointPath, query, out, authRetried, attempt+1)
	}
	if decodeErr != nil {
		return fmt.Errorf("decode baiduopen response status=%d: %w", resp.StatusCode, decodeErr)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("baiduopen api unexpected status=%d", resp.StatusCode)
	}
	if code != 0 {
		return &apiError{StatusCode: resp.StatusCode, Code: code, Message: message}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode baiduopen data: %w", err)
	}
	return nil
}

func (p *Provider) ensureAccessToken(ctx context.Context) (string, error) {
	accessToken, refreshToken, expiresAt := p.tokenState.snapshot()
	if tokenUsable(accessToken, expiresAt, time.Now()) {
		return accessToken, nil
	}
	if refreshToken == "" {
		return "", fmt.Errorf("baiduopen refresh_token is required")
	}
	if err := p.refreshAccessToken(ctx, accessToken, false); err != nil {
		return "", err
	}
	accessToken, _, _ = p.tokenState.snapshot()
	if accessToken == "" {
		return "", fmt.Errorf("baiduopen access_token unavailable")
	}
	return accessToken, nil
}

func (p *Provider) refreshAccessToken(ctx context.Context, staleAccessToken string, force bool) error {
	p.tokenState.mu.Lock()
	defer p.tokenState.mu.Unlock()

	if force {
		if p.tokenState.accessToken != "" &&
			p.tokenState.accessToken != staleAccessToken &&
			tokenUsable(p.tokenState.accessToken, p.tokenState.expiresAt, time.Now()) {
			return nil
		}
	} else if tokenUsable(p.tokenState.accessToken, p.tokenState.expiresAt, time.Now()) {
		return nil
	}
	if p.clientID == "" || p.clientSecret == "" {
		return fmt.Errorf("baiduopen client_id and client_secret are required")
	}
	if p.tokenState.refreshToken == "" {
		return fmt.Errorf("baiduopen refresh_token is required")
	}

	token, err := RefreshOAuthToken(ctx, p.httpClient, p.clientID, p.clientSecret, p.tokenState.refreshToken)
	if err != nil {
		return err
	}
	p.tokenState.accessToken = strings.TrimSpace(token.AccessToken)
	if strings.TrimSpace(token.RefreshToken) != "" {
		p.tokenState.refreshToken = strings.TrimSpace(token.RefreshToken)
	}
	p.tokenState.expiresAt = expiresAt(token.ExpiresIn, time.Now())
	if p.onTokenRefreshed != nil {
		p.onTokenRefreshed(p.tokenState.accessToken, p.tokenState.refreshToken, p.tokenState.expiresAt)
	}
	return nil
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

func (state *TokenState) snapshot() (accessToken, refreshToken, expiresAt string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.accessToken, state.refreshToken, state.expiresAt
}

func (p *Provider) getCachedNode(providerPath string) (node, bool) {
	normalized := normalizePath(providerPath)
	p.mu.RLock()
	item, ok := p.nodesByPath[normalized]
	p.mu.RUnlock()
	if ok {
		return item, true
	}
	if p.cacheStore == nil {
		return node{}, false
	}
	value, ok, err := p.cacheStore.Get(context.Background(), "node:"+normalized)
	if err != nil || !ok {
		return node{}, false
	}
	if err := json.Unmarshal([]byte(value), &item); err != nil || item.Path == "" {
		return node{}, false
	}
	p.mu.Lock()
	p.nodesByPath[normalized] = item
	p.mu.Unlock()
	return item, true
}

func (p *Provider) setCachedNode(item node) {
	if item.Path == "" {
		return
	}
	normalized := normalizePath(item.Path)
	item.Path = normalized
	p.mu.Lock()
	p.nodesByPath[normalized] = item
	p.mu.Unlock()
	if p.cacheStore != nil && item.IsDir {
		if encoded, err := json.Marshal(item); err == nil {
			_ = p.cacheStore.Set(context.Background(), "node:"+normalized, string(encoded))
		}
	}
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
		p.nodesByPath[normalizePath(item.Path)] = item
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

func AuthorizationURL(clientID, redirectURI, state string) string {
	values := url.Values{}
	values.Set("response_type", "code")
	values.Set("client_id", strings.TrimSpace(clientID))
	values.Set("redirect_uri", strings.TrimSpace(redirectURI))
	values.Set("scope", "basic,netdisk")
	values.Set("state", strings.TrimSpace(state))
	return oauthAuthorizeURL + "?" + values.Encode()
}

func ExchangeAuthorizationCode(ctx context.Context, clientID, clientSecret, code, redirectURI string) (*OAuthToken, error) {
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", strings.TrimSpace(code))
	values.Set("client_id", strings.TrimSpace(clientID))
	values.Set("client_secret", strings.TrimSpace(clientSecret))
	values.Set("redirect_uri", strings.TrimSpace(redirectURI))
	return requestOAuthToken(ctx, http.DefaultClient, values)
}

func RefreshOAuthToken(ctx context.Context, client *http.Client, clientID, clientSecret, refreshToken string) (*OAuthToken, error) {
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", strings.TrimSpace(refreshToken))
	values.Set("client_id", strings.TrimSpace(clientID))
	values.Set("client_secret", strings.TrimSpace(clientSecret))
	return requestOAuthToken(ctx, client, values)
}

func ValidateAccessToken(ctx context.Context, client *http.Client, accessToken string) error {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint, err := url.Parse(apiBaseURL + "/nas")
	if err != nil {
		return err
	}
	query := endpoint.Query()
	query.Set("method", "uinfo")
	query.Set("access_token", strings.TrimSpace(accessToken))
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("baiduopen access token validation failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read baiduopen access token validation response: %w", err)
	}
	var result apiResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decode baiduopen access token validation response status=%d: %w", resp.StatusCode, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("baiduopen access token validation unexpected status=%d", resp.StatusCode)
	}
	if code := result.code(); code != 0 {
		return &apiError{StatusCode: resp.StatusCode, Code: code, Message: result.message()}
	}
	return nil
}

func requestOAuthToken(ctx context.Context, client *http.Client, values url.Values) (*OAuthToken, error) {
	if client == nil {
		client = http.DefaultClient
	}
	var lastErr error
	for attempt := 0; attempt < maxRequestAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthTokenURL, bytes.NewBufferString(values.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", defaultUserAgent)
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			if attempt+1 < maxRequestAttempts && isRetryableTransportError(err) {
				if waitErr := waitRetry(ctx, attempt); waitErr != nil {
					return nil, waitErr
				}
				continue
			}
			return nil, fmt.Errorf("baiduopen oauth request failed: %w", err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		var result oauthResponse
		decodeErr := json.Unmarshal(body, &result)
		if attempt+1 < maxRequestAttempts && (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) {
			if waitErr := waitRetry(ctx, attempt); waitErr != nil {
				return nil, waitErr
			}
			continue
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("decode baiduopen oauth response status=%d: %w", resp.StatusCode, decodeErr)
		}
		if result.Error != "" {
			return nil, fmt.Errorf("baiduopen oauth error=%s description=%s", result.Error, result.ErrorDescription)
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return nil, fmt.Errorf("baiduopen oauth unexpected status=%d", resp.StatusCode)
		}
		if strings.TrimSpace(result.AccessToken) == "" || strings.TrimSpace(result.RefreshToken) == "" {
			return nil, fmt.Errorf("baiduopen oauth response missing access_token or refresh_token")
		}
		return &result.OAuthToken, nil
	}
	return nil, fmt.Errorf("baiduopen oauth failed after retries: %w", lastErr)
}

func (response apiResponse) code() int64 {
	if response.Errno != 0 {
		return response.Errno
	}
	return response.ErrorCode
}

func (response apiResponse) message() string {
	if strings.TrimSpace(response.ErrMsg) != "" {
		return strings.TrimSpace(response.ErrMsg)
	}
	return strings.TrimSpace(response.ErrorMsg)
}

func nodeFromFileItem(parentPath string, item fileItem) node {
	name := strings.TrimSpace(item.ServerFilename)
	itemPath := normalizePath(item.Path)
	if itemPath == "/" || name == "" {
		itemPath = normalizePath(path.Join(parentPath, name))
	}
	modTime := item.ServerMTime
	if modTime <= 0 {
		modTime = item.LocalMTime
	}
	result := node{
		ID:       string(item.FSID),
		ParentID: normalizePath(parentPath),
		Path:     itemPath,
		Name:     name,
		MD5:      strings.TrimSpace(item.MD5),
		IsDir:    item.IsDir != 0,
		Size:     parseInt64(string(item.Size)),
		Category: item.Category,
	}
	if modTime > 0 {
		result.ModTime = time.Unix(modTime, 0).UTC().Format(time.RFC3339)
	}
	if !result.IsDir {
		result.MimeType = mime.TypeByExtension(strings.ToLower(path.Ext(name)))
	}
	return result
}

func toEntry(item node) provider.Entry {
	metadata := map[string]string{
		"entry_type": "file",
		"fs_id":      item.ID,
	}
	if item.IsDir {
		metadata["entry_type"] = "dir"
	}
	if item.ParentID != "" {
		metadata["parent_id"] = item.ParentID
	}
	if item.MD5 != "" {
		metadata["md5"] = item.MD5
	}
	if item.ModTime != "" {
		metadata["mtime"] = item.ModTime
	}
	if item.MimeType != "" {
		metadata["mime_type"] = item.MimeType
	}
	if item.Size > 0 {
		metadata["size"] = strconv.FormatInt(item.Size, 10)
	}
	if item.Category != 0 {
		metadata["category"] = strconv.Itoa(item.Category)
	}
	return provider.Entry{
		ID:       item.ID,
		Name:     item.Name,
		Path:     item.Path,
		IsDir:    item.IsDir,
		Size:     item.Size,
		ModTime:  item.ModTime,
		MimeType: item.MimeType,
		Metadata: metadata,
	}
}

func appendAccessToken(rawURL, accessToken string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("parse baiduopen download url: %w", err)
	}
	query := parsed.Query()
	if query.Get("access_token") == "" {
		query.Set("access_token", strings.TrimSpace(accessToken))
		parsed.RawQuery = query.Encode()
	}
	return parsed.String(), nil
}

func cloneValues(source url.Values) url.Values {
	result := make(url.Values, len(source)+1)
	for key, values := range source {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func normalizePath(value string) string {
	clean := path.Clean("/" + strings.TrimSpace(value))
	if clean == "." {
		return "/"
	}
	return clean
}

func splitPathSegments(value string) []string {
	trimmed := strings.Trim(normalizePath(value), "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func hasPathPrefix(value, prefix string) bool {
	value = normalizePath(value)
	prefix = normalizePath(prefix)
	return value == prefix || strings.HasPrefix(value, prefix+"/")
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func tokenUsable(accessToken, expiresAtValue string, now time.Time) bool {
	if strings.TrimSpace(accessToken) == "" {
		return false
	}
	expiresAtValue = strings.TrimSpace(expiresAtValue)
	if expiresAtValue == "" {
		return true
	}
	expiry, err := time.Parse(time.RFC3339, expiresAtValue)
	if err != nil {
		return false
	}
	return now.Add(tokenExpiryLeeway).Before(expiry)
}

func expiresAt(expiresIn int64, now time.Time) string {
	if expiresIn <= 0 {
		return ""
	}
	return now.UTC().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339)
}

func shouldRefreshToken(statusCode int, code int64) bool {
	return statusCode == http.StatusUnauthorized || code == -6 || code == 110 || code == 111
}

func isRetryableAPIResponse(statusCode int, code int64, message string) bool {
	if statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError {
		return true
	}
	if code == 429 || code == 31034 {
		return true
	}
	message = strings.ToLower(strings.TrimSpace(message))
	for _, marker := range []string{"频繁", "频控", "稍后", "rate limit", "too many", "temporarily unavailable", "timeout"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
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

func waitRetry(ctx context.Context, attempt int) error {
	timer := time.NewTimer(retryBaseDelay * time.Duration(1<<attempt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

package pan123

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
	apiBaseURL = "https://open-api.123pan.com"

	accessTokenPath  = "/api/v1/access_token"
	fileListPath     = "/api/v2/file/list"
	downloadInfoPath = "/api/v1/file/download_info"

	platformHeaderValue = "open_platform"
	defaultUserAgent    = "NyaMedia/123pan"
	pageSize            = 100
	requestTimeout      = 2 * time.Minute

	maxRequestAttempts = 3
	retryBaseDelay     = 250 * time.Millisecond
	childrenCacheTTL   = 10 * time.Minute
	tokenExpiryLeeway  = time.Minute
)

type CacheStore interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
	SetWithTTL(ctx context.Context, key, value string, ttl time.Duration) error
}

// TokenState coordinates access-token refreshes across Provider instances that
// use the same 123pan application credentials.
type TokenState struct {
	mu          sync.Mutex
	accessToken string
	expiresAt   string
}

func NewTokenState(accessToken, expiresAt string) *TokenState {
	return &TokenState{
		accessToken: strings.TrimSpace(accessToken),
		expiresAt:   strings.TrimSpace(expiresAt),
	}
}

type Provider struct {
	id               string
	rootPath         string
	clientID         string
	clientSecret     string
	httpClient       *http.Client
	onTokenRefreshed func(accessToken, expiresAt string)
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
	ETag     string `json:"etag,omitempty"`
	IsDir    bool   `json:"is_dir"`
	Size     int64  `json:"size,omitempty"`
	ModTime  string `json:"mod_time,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type childrenCacheEntry struct {
	Items []node `json:"items"`
}

type apiResponse struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type tokenResponse struct {
	AccessToken flexibleString `json:"accessToken"`
	ExpiredAt   flexibleString `json:"expiredAt"`
}

type fileListResponse struct {
	LastFileID flexibleString `json:"lastFileId"`
	FileList   []fileItem     `json:"fileList"`
}

type fileItem struct {
	FileID       flexibleString `json:"fileId"`
	Filename     string         `json:"filename"`
	Type         int            `json:"type"`
	ETag         string         `json:"etag"`
	Size         flexibleString `json:"size"`
	ParentFileID flexibleString `json:"parentFileId"`
	Category     int            `json:"category"`
	Status       int            `json:"status"`
	Trashed      int            `json:"trashed"`
	CreateAt     flexibleString `json:"createAt"`
	UpdateAt     flexibleString `json:"updateAt"`
}

type downloadInfoResponse struct {
	DownloadURL string         `json:"downloadUrl"`
	ExpireAt    flexibleString `json:"expireAt"`
}

type flexibleString string

func (value *flexibleString) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*value = ""
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*value = flexibleString(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	*value = flexibleString(number.String())
	return nil
}

func New(
	id string,
	rootPath string,
	clientID string,
	clientSecret string,
	accessToken string,
	accessTokenExpiresAt string,
	onTokenRefreshed func(accessToken, expiresAt string),
	cacheStores ...CacheStore,
) *Provider {
	return NewWithTokenState(
		id,
		rootPath,
		clientID,
		clientSecret,
		NewTokenState(accessToken, accessTokenExpiresAt),
		onTokenRefreshed,
		cacheStores...,
	)
}

func NewWithTokenState(
	id string,
	rootPath string,
	clientID string,
	clientSecret string,
	tokenState *TokenState,
	onTokenRefreshed func(accessToken, expiresAt string),
	cacheStores ...CacheStore,
) *Provider {
	cleanRoot := normalizePath(rootPath)
	if tokenState == nil {
		tokenState = NewTokenState("", "")
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
	return "123pan"
}

func (p *Provider) List(ctx context.Context, providerPath string) ([]provider.Entry, error) {
	parent, err := p.resolveDir(ctx, providerPath)
	if err != nil {
		return nil, err
	}
	nodes, err := p.listNodesByID(ctx, parent)
	if err != nil {
		return nil, err
	}
	return entriesFromNodes(nodes), nil
}

func (p *Provider) Stat(ctx context.Context, providerPath string) (*provider.Entry, error) {
	item, err := p.resolveNode(ctx, providerPath)
	if err != nil {
		return nil, err
	}
	entry := toEntry(item)
	return &entry, nil
}

func (p *Provider) GetDirectLink(ctx context.Context, providerPath string) (*provider.DirectLinkResult, error) {
	item, err := p.resolveNode(ctx, providerPath)
	if err != nil {
		return nil, err
	}
	return p.directLinkForNode(ctx, item)
}

func (p *Provider) GetDirectLinkForEntry(ctx context.Context, input provider.DirectLinkInput) (*provider.DirectLinkResult, error) {
	providerPath := normalizePath(input.Path)
	if err := p.validatePathWithinRoot(providerPath); err != nil {
		return nil, err
	}
	fileID := strings.TrimSpace(input.ProviderEntryID)
	if fileID == "" {
		fileID = strings.TrimSpace(input.Metadata["file_id"])
	}
	if fileID != "" {
		item := node{
			ID:       fileID,
			ParentID: strings.TrimSpace(input.Metadata["parent_id"]),
			Path:     providerPath,
			Name:     path.Base(providerPath),
			ETag:     strings.TrimSpace(input.Metadata["etag"]),
			IsDir:    strings.EqualFold(input.Metadata["entry_type"], "dir"),
			Size:     parseInt64(input.Metadata["size"]),
			ModTime:  strings.TrimSpace(input.Metadata["mtime"]),
			MimeType: strings.TrimSpace(input.Metadata["mime_type"]),
		}
		return p.directLinkForNode(ctx, item)
	}
	p.LoadPersistedEntryMetadata(providerPath, input.ProviderEntryID, input.Metadata)
	return p.GetDirectLink(ctx, providerPath)
}

func (p *Provider) directLinkForNode(ctx context.Context, item node) (*provider.DirectLinkResult, error) {
	if item.IsDir {
		return nil, fmt.Errorf("path %s is a directory", item.Path)
	}
	if strings.TrimSpace(item.ID) == "" {
		return nil, fmt.Errorf("123pan file id unavailable for %s", item.Path)
	}
	info, err := p.getDownloadInfo(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(info.DownloadURL) == "" {
		return nil, fmt.Errorf("123pan download url unavailable for %s", item.Path)
	}
	return &provider.DirectLinkResult{
		URL:      info.DownloadURL,
		ExpireAt: normalizeTimestamp(string(info.ExpireAt)),
	}, nil
}

func (p *Provider) LoadPersistedEntryMetadata(providerPath string, providerEntryID string, metadata map[string]string) {
	normalized := normalizePath(providerPath)
	if normalized == "/" || p.validatePathWithinRoot(normalized) != nil {
		return
	}
	item := node{
		ID:       strings.TrimSpace(providerEntryID),
		ParentID: strings.TrimSpace(metadata["parent_id"]),
		Path:     normalized,
		Name:     path.Base(normalized),
		ETag:     strings.TrimSpace(metadata["etag"]),
		IsDir:    strings.EqualFold(metadata["entry_type"], "dir"),
		Size:     parseInt64(metadata["size"]),
		ModTime:  strings.TrimSpace(metadata["mtime"]),
		MimeType: strings.TrimSpace(metadata["mime_type"]),
	}
	if item.ID == "" {
		item.ID = strings.TrimSpace(metadata["file_id"])
	}
	if item.ID == "" {
		return
	}
	p.setCachedNode(item)
}

func (p *Provider) CheckStatus(ctx context.Context) (model.ProviderStatus, string) {
	root, err := p.resolveRoot(ctx)
	if err != nil {
		return model.ProviderStatusError, err.Error()
	}
	if _, err := p.listFilesPage(ctx, root.ID, "0", 1); err != nil {
		return model.ProviderStatusError, err.Error()
	}
	return model.ProviderStatusHealthy, ""
}

func (p *Provider) WalkFiles(ctx context.Context, sourcePath string, options provider.WalkOptions, fn func(entry provider.Entry) error) error {
	root, err := p.resolveDir(ctx, sourcePath)
	if err != nil {
		return err
	}
	return p.walkNode(ctx, root, options, fn)
}

func (p *Provider) walkNode(ctx context.Context, current node, options provider.WalkOptions, fn func(entry provider.Entry) error) error {
	items, err := p.listNodesByID(ctx, current)
	if err != nil {
		return err
	}
	children := entriesFromNodes(items)
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
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if item.IsDir {
			if err := p.walkNode(ctx, item, options, fn); err != nil {
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

func (p *Provider) listNodesByID(ctx context.Context, parent node) ([]node, error) {
	if items, ok := p.getCachedChildren(ctx, parent.Path); ok {
		return items, nil
	}
	files, err := p.listFiles(ctx, parent.ID)
	if err != nil {
		return nil, err
	}
	items := make([]node, 0, len(files))
	for _, item := range files {
		if item.Trashed != 0 {
			continue
		}
		resolved := nodeFromFileItem(parent.Path, item)
		if resolved.ID == "" || resolved.Name == "" {
			continue
		}
		p.setCachedNode(resolved)
		items = append(items, resolved)
	}
	p.setCachedChildren(ctx, parent.Path, items)
	return items, nil
}

func (p *Provider) listFiles(ctx context.Context, parentFileID string) ([]fileItem, error) {
	items := make([]fileItem, 0, pageSize)
	cursor := "0"
	for {
		page, err := p.listFilesPage(ctx, parentFileID, cursor, pageSize)
		if err != nil {
			return nil, err
		}
		items = append(items, page.FileList...)
		nextCursor := strings.TrimSpace(string(page.LastFileID))
		if nextCursor == "" || nextCursor == "-1" || nextCursor == cursor {
			break
		}
		cursor = nextCursor
	}
	return items, nil
}

func (p *Provider) listFilesPage(ctx context.Context, parentFileID, lastFileID string, limit int) (*fileListResponse, error) {
	query := url.Values{}
	query.Set("parentFileId", parentFileID)
	query.Set("limit", strconv.Itoa(limit))
	query.Set("lastFileId", lastFileID)
	var result fileListResponse
	if err := p.doAPI(ctx, http.MethodGet, fileListPath, query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (p *Provider) getDownloadInfo(ctx context.Context, fileID string) (*downloadInfoResponse, error) {
	query := url.Values{}
	query.Set("fileId", fileID)
	var result downloadInfoResponse
	if err := p.doAPI(ctx, http.MethodGet, downloadInfoPath, query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (p *Provider) resolveNode(ctx context.Context, providerPath string) (node, error) {
	normalized := normalizePath(providerPath)
	if err := p.validatePathWithinRoot(normalized); err != nil {
		return node{}, err
	}
	if normalized == "/" {
		return p.resolveRoot(ctx)
	}
	if item, ok := p.getCachedNode(normalized); ok {
		return item, nil
	}
	parentPath := normalizePath(path.Dir(normalized))
	parent, err := p.resolveDir(ctx, parentPath)
	if err != nil {
		return node{}, err
	}
	return p.findNamedChild(ctx, parent, path.Base(normalized))
}

func (p *Provider) resolveDir(ctx context.Context, providerPath string) (node, error) {
	normalized := normalizePath(providerPath)
	if err := p.validatePathWithinRoot(normalized); err != nil {
		return node{}, err
	}
	root, err := p.resolveRoot(ctx)
	if err != nil {
		return node{}, err
	}
	if normalized == p.rootPath {
		return root, nil
	}
	if cached, ok := p.getCachedNode(normalized); ok {
		if !cached.IsDir {
			return node{}, fmt.Errorf("path %s is not a directory", normalized)
		}
		return cached, nil
	}

	current := root
	for _, segment := range p.pathSegmentsFromRoot(normalized) {
		child, err := p.findNamedChild(ctx, current, segment)
		if err != nil {
			return node{}, err
		}
		if !child.IsDir {
			return node{}, fmt.Errorf("path %s is not a directory", normalized)
		}
		current = child
	}
	return current, nil
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

	current := node{ID: "0", Path: "/", Name: "/", IsDir: true}
	for _, segment := range splitPathSegments(p.rootPath) {
		child, err := p.findNamedChild(ctx, current, segment)
		if err != nil {
			return node{}, fmt.Errorf("123pan root path not found: %s: %w", p.rootPath, err)
		}
		if !child.IsDir {
			return node{}, fmt.Errorf("123pan root path is not a directory: %s", p.rootPath)
		}
		current = child
	}
	p.setCachedNode(current)
	return current, nil
}

func (p *Provider) findNamedChild(ctx context.Context, parent node, name string) (node, error) {
	childPath := normalizePath(path.Join(parent.Path, name))
	if cached, ok := p.getCachedNode(childPath); ok {
		return cached, nil
	}
	items, err := p.listNodesByID(ctx, parent)
	if err != nil {
		return node{}, err
	}
	for _, item := range items {
		if item.Name == name {
			return item, nil
		}
	}
	return node{}, fmt.Errorf("123pan path not found: %s", childPath)
}

func (p *Provider) pathSegmentsFromRoot(providerPath string) []string {
	segments := splitPathSegments(providerPath)
	if p.rootPath == "/" {
		return segments
	}
	rootSegments := splitPathSegments(p.rootPath)
	if len(segments) <= len(rootSegments) {
		return nil
	}
	return segments[len(rootSegments):]
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
	method string,
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
	req, err := http.NewRequestWithContext(ctx, method, apiBaseURL+endpointPath, nil)
	if err != nil {
		return err
	}
	if query != nil {
		req.URL.RawQuery = query.Encode()
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Platform", platformHeaderValue)
	req.Header.Set("User-Agent", requestUserAgent(ctx))
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
		return fmt.Errorf("123pan request failed: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read 123pan response: %w", err)
	}

	var envelope apiResponse
	decodeErr := json.Unmarshal(responseBody, &envelope)
	if !authRetried && shouldRefreshToken(resp.StatusCode, envelope.Code) {
		if err := p.refreshAccessToken(ctx, accessToken, true); err != nil {
			return err
		}
		return p.doAPIWithRetry(ctx, method, endpointPath, query, out, true, 0)
	}
	if attempt+1 < maxRequestAttempts && isRetryableAPIResponse(resp.StatusCode, envelope.Code, envelope.Message) {
		if waitErr := waitRetry(ctx, attempt); waitErr != nil {
			return waitErr
		}
		return p.doAPIWithRetry(ctx, method, endpointPath, query, out, authRetried, attempt+1)
	}
	if decodeErr != nil {
		return fmt.Errorf("decode 123pan response status=%d: %w", resp.StatusCode, decodeErr)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("123pan api unexpected status=%d", resp.StatusCode)
	}
	if envelope.Code != 0 {
		return fmt.Errorf("123pan api error code=%d message=%s", envelope.Code, envelope.Message)
	}
	if out == nil || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decode 123pan data: %w", err)
	}
	return nil
}

func (p *Provider) ensureAccessToken(ctx context.Context) (string, error) {
	accessToken, expiresAt := p.tokenState.snapshot()
	if tokenUsable(accessToken, expiresAt, time.Now()) {
		return accessToken, nil
	}
	if err := p.refreshAccessToken(ctx, accessToken, false); err != nil {
		return "", err
	}
	accessToken, _ = p.tokenState.snapshot()
	if accessToken == "" {
		return "", fmt.Errorf("123pan access token unavailable")
	}
	return accessToken, nil
}

func (p *Provider) refreshAccessToken(ctx context.Context, staleAccessToken string, force bool) error {
	p.tokenState.mu.Lock()
	defer p.tokenState.mu.Unlock()

	currentAccessToken := p.tokenState.accessToken
	currentExpiresAt := p.tokenState.expiresAt
	if force {
		if currentAccessToken != "" && currentAccessToken != staleAccessToken && tokenUsable(currentAccessToken, currentExpiresAt, time.Now()) {
			return nil
		}
	} else if tokenUsable(currentAccessToken, currentExpiresAt, time.Now()) {
		return nil
	}
	if p.clientID == "" || p.clientSecret == "" {
		return fmt.Errorf("123pan client_id and client_secret are required")
	}

	payload, err := json.Marshal(map[string]string{
		"clientID":     p.clientID,
		"clientSecret": p.clientSecret,
	})
	if err != nil {
		return err
	}
	var result tokenResponse
	for attempt := 0; attempt < maxRequestAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBaseURL+accessTokenPath, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Platform", platformHeaderValue)
		req.Header.Set("User-Agent", requestUserAgent(ctx))

		resp, err := p.httpClient.Do(req)
		if err != nil {
			if attempt+1 < maxRequestAttempts && isRetryableTransportError(err) {
				if waitErr := waitRetry(ctx, attempt); waitErr != nil {
					return waitErr
				}
				continue
			}
			return fmt.Errorf("123pan authentication failed: %w", err)
		}
		responseBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read 123pan authentication response: %w", readErr)
		}

		var envelope apiResponse
		decodeErr := json.Unmarshal(responseBody, &envelope)
		if attempt+1 < maxRequestAttempts && isRetryableAPIResponse(resp.StatusCode, envelope.Code, envelope.Message) {
			if waitErr := waitRetry(ctx, attempt); waitErr != nil {
				return waitErr
			}
			continue
		}
		if decodeErr != nil {
			return fmt.Errorf("decode 123pan authentication response status=%d: %w", resp.StatusCode, decodeErr)
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || envelope.Code != 0 {
			return fmt.Errorf("123pan authentication rejected status=%d code=%d message=%s", resp.StatusCode, envelope.Code, envelope.Message)
		}
		if err := json.Unmarshal(envelope.Data, &result); err != nil {
			return fmt.Errorf("decode 123pan token data: %w", err)
		}
		accessToken := strings.TrimSpace(string(result.AccessToken))
		expiresAt := strings.TrimSpace(string(result.ExpiredAt))
		if accessToken == "" {
			return fmt.Errorf("123pan authentication response missing accessToken")
		}
		p.setAccessTokenLocked(accessToken, expiresAt)
		return nil
	}
	return fmt.Errorf("123pan authentication failed after %d attempts", maxRequestAttempts)
}

func (state *TokenState) snapshot() (string, string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.accessToken, state.expiresAt
}

func (p *Provider) setAccessTokenLocked(accessToken, expiresAt string) {
	p.tokenState.accessToken = strings.TrimSpace(accessToken)
	p.tokenState.expiresAt = strings.TrimSpace(expiresAt)
	callback := p.onTokenRefreshed
	updatedToken := p.tokenState.accessToken
	updatedExpiresAt := p.tokenState.expiresAt
	if callback != nil {
		callback(updatedToken, updatedExpiresAt)
	}
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
	if err := json.Unmarshal([]byte(value), &item); err != nil || item.ID == "" || item.Path == "" {
		return node{}, false
	}
	p.mu.Lock()
	p.nodesByPath[normalized] = item
	p.mu.Unlock()
	return item, true
}

func (p *Provider) setCachedNode(item node) {
	if item.ID == "" || item.Path == "" {
		return
	}
	normalized := normalizePath(item.Path)
	item.Path = normalized
	p.mu.Lock()
	p.nodesByPath[normalized] = item
	p.mu.Unlock()
	if item.IsDir {
		p.setPersistentCache("node:"+normalized, item)
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

func nodeFromFileItem(parentPath string, item fileItem) node {
	name := strings.TrimSpace(item.Filename)
	isDir := item.Type == 1
	result := node{
		ID:       strings.TrimSpace(string(item.FileID)),
		ParentID: strings.TrimSpace(string(item.ParentFileID)),
		Path:     normalizePath(path.Join(parentPath, name)),
		Name:     name,
		ETag:     strings.TrimSpace(item.ETag),
		IsDir:    isDir,
		Size:     parseInt64(string(item.Size)),
		ModTime:  normalizeTimestamp(string(item.UpdateAt), string(item.CreateAt)),
	}
	if !isDir {
		result.MimeType = mime.TypeByExtension(strings.ToLower(path.Ext(name)))
	}
	return result
}

func entriesFromNodes(items []node) []provider.Entry {
	entries := make([]provider.Entry, 0, len(items))
	for _, item := range items {
		entries = append(entries, toEntry(item))
	}
	return entries
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
	metadata := map[string]string{
		"entry_type": "file",
		"file_id":    item.ID,
	}
	if item.IsDir {
		metadata["entry_type"] = "dir"
	}
	if item.ParentID != "" {
		metadata["parent_id"] = item.ParentID
	}
	if item.ETag != "" {
		metadata["etag"] = item.ETag
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
	valueSegments := splitPathSegments(value)
	prefixSegments := splitPathSegments(prefix)
	if len(prefixSegments) == 0 {
		return true
	}
	if len(valueSegments) < len(prefixSegments) {
		return false
	}
	for index := range prefixSegments {
		if valueSegments[index] != prefixSegments[index] {
			return false
		}
	}
	return true
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func normalizeTimestamp(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if parsed, ok := parseTime(value); ok {
			return parsed.UTC().Format(time.RFC3339)
		}
		return value
	}
	return ""
}

func parseTime(value string) (time.Time, bool) {
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	if unixValue, err := strconv.ParseInt(value, 10, 64); err == nil && unixValue > 0 {
		return time.Unix(unixValue, 0), true
	}
	return time.Time{}, false
}

func tokenUsable(accessToken, expiresAt string, now time.Time) bool {
	if strings.TrimSpace(accessToken) == "" {
		return false
	}
	expiresAt = strings.TrimSpace(expiresAt)
	if expiresAt == "" {
		return true
	}
	expiry, ok := parseTime(expiresAt)
	if !ok {
		return false
	}
	return now.Add(tokenExpiryLeeway).Before(expiry)
}

func requestUserAgent(ctx context.Context) string {
	if userAgent := provider.RequestUserAgentFromContext(ctx); userAgent != "" {
		return userAgent
	}
	return defaultUserAgent
}

func shouldRefreshToken(statusCode int, code int64) bool {
	if statusCode == http.StatusUnauthorized || code == http.StatusUnauthorized {
		return true
	}
	return code >= 40100 && code < 40200
}

func isRetryableAPIResponse(statusCode int, code int64, message string) bool {
	if statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError {
		return true
	}
	if code == http.StatusTooManyRequests || (code >= 42900 && code < 43000) {
		return true
	}
	message = strings.ToLower(strings.TrimSpace(message))
	for _, marker := range []string{"rate limit", "too many", "temporarily unavailable", "timeout"} {
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

var (
	_ provider.Provider                       = (*Provider)(nil)
	_ provider.StatusProvider                 = (*Provider)(nil)
	_ provider.ScanProvider                   = (*Provider)(nil)
	_ provider.PersistedEntryMetadataProvider = (*Provider)(nil)
)

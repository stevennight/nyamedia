package open115

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"emby115/internal/model"
	"emby115/internal/provider"
)

const (
	apiBaseURL  = "https://proapi.115.com"
	authBaseURL = "https://passportapi.115.com"
	defaultUA   = "emby115/0.1"
	pageSize    = 1000
)

type Provider struct {
	id               string
	rootPath         string
	httpClient       *http.Client
	onTokenRefreshed func(accessToken, refreshToken string)

	mu           sync.RWMutex
	accessToken  string
	refreshToken string
	cache        map[string]node
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

func New(id, rootPath, accessToken, refreshToken string, onTokenRefreshed func(accessToken, refreshToken string)) *Provider {
	cleanRoot := normalizePath(rootPath)
	if cleanRoot == "" {
		cleanRoot = "/"
	}

	p := &Provider{
		id:               id,
		rootPath:         cleanRoot,
		httpClient:       &http.Client{Timeout: 30 * time.Second},
		onTokenRefreshed: onTokenRefreshed,
		accessToken:      strings.TrimSpace(accessToken),
		refreshToken:     strings.TrimSpace(refreshToken),
		cache:            make(map[string]node),
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

	items := make([]provider.Entry, 0)
	offset := int64(0)
	for {
		resp, err := p.getFiles(ctx, dirNode.ID, offset, pageSize)
		if err != nil {
			return nil, err
		}
		for _, item := range resp.Data {
			entry := p.entryFromFileItem(providerPath, item)
			items = append(items, entry)
		}
		if len(resp.Data) < pageSize {
			break
		}
		offset += int64(len(resp.Data))
	}

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
			return &provider.DirectLinkResult{URL: videoURL, SupportsRange: true}, nil
		}
	}

	downloadURL, err := p.getDownloadURL(ctx, item.PickCode)
	if err != nil {
		return nil, err
	}
	return &provider.DirectLinkResult{URL: downloadURL, SupportsRange: true}, nil
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
	if _, err := p.resolveRoot(ctx); err != nil {
		return model.ProviderStatusError, err.Error()
	}
	return model.ProviderStatusHealthy, ""
}

func (p *Provider) WalkFiles(ctx context.Context, sourcePath string, fn func(entry provider.Entry) error) error {
	root, err := p.resolveDir(ctx, sourcePath)
	if err != nil {
		return err
	}
	if err := fn(toEntry(root)); err != nil {
		return err
	}
	return p.walk(ctx, normalizePath(sourcePath), fn)
}

func (p *Provider) walk(ctx context.Context, current string, fn func(entry provider.Entry) error) error {
	items, err := p.List(ctx, current)
	if err != nil {
		return err
	}
	for _, item := range items {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if item.IsDir {
			if err := fn(item); err != nil {
				return err
			}
			if err := p.walk(ctx, item.Path, fn); err != nil {
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
	if cached, ok := p.getCached("/"); ok && (cached.ID != "" || p.rootPath == "/") {
		return cached, nil
	}

	info, err := p.getInfoByPath(ctx, p.rootPath)
	if err != nil {
		return node{}, err
	}
	rootNode := p.nodeFromInfo("/", info)
	rootNode.Name = "/"
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

func (p *Provider) doAPI(ctx context.Context, method, endpoint string, query url.Values, form map[string]string, out any) error {
	return p.doAPIWithRetry(ctx, method, endpoint, query, form, out, false)
}

func (p *Provider) doRawAPI(ctx context.Context, method, endpoint string, query url.Values, form map[string]string, out any) error {
	return p.doRawAPIWithRetry(ctx, method, endpoint, query, form, out, false)
}

func (p *Provider) doAPIWithRetry(ctx context.Context, method, endpoint string, query url.Values, form map[string]string, out any, retried bool) error {
	if p.accessTokenValue() == "" && p.refreshTokenValue() != "" {
		if err := p.refreshAccessToken(ctx); err != nil {
			return err
		}
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
	req.Header.Set("User-Agent", defaultUA)
	if form != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if token := p.accessTokenValue(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var envelope apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if !envelope.State {
		if !retried && shouldRefresh(envelope.Code, resp.StatusCode) && p.refreshTokenValue() != "" {
			if err := p.refreshAccessToken(ctx); err != nil {
				return err
			}
			return p.doAPIWithRetry(ctx, method, endpoint, query, form, out, true)
		}
		return fmt.Errorf("115open api error code=%d message=%s", envelope.Code, envelope.Message)
	}
	if out == nil {
		return nil
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	return json.Unmarshal(envelope.Data, out)
}

func (p *Provider) doRawAPIWithRetry(ctx context.Context, method, endpoint string, query url.Values, form map[string]string, out any, retried bool) error {
	if p.accessTokenValue() == "" && p.refreshTokenValue() != "" {
		if err := p.refreshAccessToken(ctx); err != nil {
			return err
		}
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
	req.Header.Set("User-Agent", defaultUA)
	if form != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if token := p.accessTokenValue(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var meta apiResponse
	if err := json.Unmarshal(responseBody, &meta); err != nil {
		return err
	}
	if !meta.State {
		if !retried && shouldRefresh(meta.Code, resp.StatusCode) && p.refreshTokenValue() != "" {
			if err := p.refreshAccessToken(ctx); err != nil {
				return err
			}
			return p.doRawAPIWithRetry(ctx, method, endpoint, query, form, out, true)
		}
		return fmt.Errorf("115open api error code=%d message=%s", meta.Code, meta.Message)
	}

	return json.Unmarshal(responseBody, out)
}

func (p *Provider) refreshAccessToken(ctx context.Context) error {
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
	req.Header.Set("User-Agent", defaultUA)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var envelope authResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if envelope.Code != 0 {
		if envelope.Error != "" {
			return fmt.Errorf("115open auth error code=%d errno=%d error=%s", envelope.Code, envelope.Errno, envelope.Error)
		}
		return fmt.Errorf("115open auth error code=%d message=%s", envelope.Code, envelope.Message)
	}

	var token tokenResponse
	if err := json.Unmarshal(envelope.Data, &token); err != nil {
		return err
	}
	p.setTokens(token.AccessToken, token.RefreshToken)
	return nil
}

func (p *Provider) setTokens(accessToken, refreshToken string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.accessToken = strings.TrimSpace(accessToken)
	if strings.TrimSpace(refreshToken) != "" {
		p.refreshToken = strings.TrimSpace(refreshToken)
	}
	if p.onTokenRefreshed != nil {
		p.onTokenRefreshed(p.accessToken, p.refreshToken)
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
	p.mu.RLock()
	defer p.mu.RUnlock()
	item, ok := p.cache[normalizePath(providerPath)]
	return item, ok
}

func (p *Provider) setCached(item node) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache[normalizePath(item.Path)] = item
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

func (p *Provider) entryFromFileItem(parentPath string, item fileItem) provider.Entry {
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
	p.setCached(nodeItem)
	return toEntry(nodeItem)
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

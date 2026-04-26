package cookie115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	pan115 "github.com/SheltonZhu/115driver/pkg/driver"

	"NyaMedia/internal/model"
	"NyaMedia/internal/provider"
)

const defaultUserAgent = "Mozilla/5.0"

const (
	requestInterval  = 1 * time.Second
	maxListRetries   = 3
	listPageSize     = 100
	childrenCacheTTL = 10 * time.Minute
)

type CacheStore interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
	SetWithTTL(ctx context.Context, key, value string, ttl time.Duration) error
}

type Provider struct {
	id        string
	rootPath  string
	userAgent string
	client    *pan115.Pan115Client

	requestMu   sync.Mutex
	lastRequest time.Time
	cacheMu     sync.RWMutex
	nodesByPath map[string]node
	cacheStore  CacheStore
}

type node struct {
	ID       string
	ParentID string
	Path     string
	Name     string
	PickCode string
	IsDir    bool
	Size     int64
	ModTime  string
	MimeType string
}

type childrenCacheEntry struct {
	Items []node `json:"items"`
}

func New(id, rootPath, cookieValue, userAgent string, cacheStore ...CacheStore) (*Provider, error) {
	credential := &pan115.Credential{}
	if err := credential.FromCookie(cookieValue); err != nil {
		return nil, fmt.Errorf("parse 115 cookie: %w", err)
	}
	ua := strings.TrimSpace(userAgent)
	if ua == "" {
		ua = defaultUserAgent
	}
	client := pan115.New().SetUserAgent(ua)
	client.ImportCredential(credential)
	provider := &Provider{
		id:          id,
		rootPath:    normalizePath(rootPath),
		userAgent:   ua,
		client:      client,
		nodesByPath: make(map[string]node),
	}
	if len(cacheStore) > 0 {
		provider.cacheStore = cacheStore[0]
	}
	return provider, nil
}

func (p *Provider) ID() string {
	return p.id
}

func (p *Provider) Type() string {
	return "115cookie"
}

func (p *Provider) List(ctx context.Context, providerPath string) ([]provider.Entry, error) {
	dirNode, err := p.resolveDir(ctx, providerPath)
	if err != nil {
		return nil, err
	}
	children, err := p.listNodesByID(ctx, dirNode)
	if err != nil {
		return nil, err
	}
	items := make([]provider.Entry, 0, len(children))
	for _, item := range children {
		items = append(items, toEntry(item))
	}
	return items, nil
}

func (p *Provider) Stat(ctx context.Context, providerPath string) (*provider.Entry, error) {
	resolved, err := p.resolveNode(ctx, providerPath)
	if err != nil {
		return nil, err
	}
	entry := toEntry(resolved)
	return &entry, nil
}

func (p *Provider) GetDirectLink(ctx context.Context, providerPath string) (*provider.DirectLinkResult, error) {
	resolved, err := p.resolveNode(ctx, providerPath)
	if err != nil {
		return nil, err
	}
	if resolved.IsDir {
		return nil, fmt.Errorf("path %s is a directory", resolved.Path)
	}
	if resolved.PickCode == "" {
		return nil, fmt.Errorf("pick code unavailable for %s", resolved.Path)
	}
	if err := p.waitRequest(ctx); err != nil {
		return nil, err
	}
	userAgent := provider.RequestUserAgentFromContext(ctx)
	if userAgent == "" {
		userAgent = p.userAgent
	}
	info, err := p.client.DownloadWithUA(resolved.PickCode, userAgent)
	if err != nil {
		return nil, fmt.Errorf("get 115 direct link %s: %w", resolved.Path, err)
	}
	return &provider.DirectLinkResult{
		URL:           info.Url.Url,
		Headers:       headerMap(info.Header),
		SupportsRange: true,
	}, nil
}

func (p *Provider) GetDirectLinkForEntry(ctx context.Context, input provider.DirectLinkInput) (*provider.DirectLinkResult, error) {
	providerPath := normalizePath(input.Path)
	pickCode := strings.TrimSpace(input.Metadata["pick_code"])
	if pickCode == "" {
		p.LoadPersistedEntryMetadata(providerPath, input.ProviderEntryID, input.Metadata)
		return p.GetDirectLink(ctx, providerPath)
	}
	if err := p.waitRequest(ctx); err != nil {
		return nil, err
	}
	userAgent := provider.RequestUserAgentFromContext(ctx)
	if userAgent == "" {
		userAgent = p.userAgent
	}
	info, err := p.client.DownloadWithUA(pickCode, userAgent)
	if err != nil {
		return nil, fmt.Errorf("get 115 direct link %s: %w", providerPath, err)
	}
	return &provider.DirectLinkResult{
		URL:           info.Url.Url,
		Headers:       headerMap(info.Header),
		SupportsRange: true,
	}, nil
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
	if item.ID == "" && item.PickCode == "" {
		return
	}
	p.setCachedNode(item)
}

func (p *Provider) CheckStatus(ctx context.Context) (model.ProviderStatus, string) {
	if err := p.client.CookieCheck(); err != nil {
		return model.ProviderStatusError, err.Error()
	}
	if _, err := p.resolveDir(ctx, "/"); err != nil {
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
	return p.walkNode(ctx, root, fn)
}

func (p *Provider) walkNode(ctx context.Context, current node, fn func(entry provider.Entry) error) error {
	items, err := p.listNodesByID(ctx, current)
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
			if err := fn(toEntry(item)); err != nil {
				return err
			}
			if err := p.walkNode(ctx, item, fn); err != nil {
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
	files, err := p.listFiles(ctx, parent.ID, parent.Path)
	if err != nil {
		return nil, err
	}
	items := make([]node, 0, len(*files))
	for _, item := range *files {
		resolved := p.nodeFromFile(parent.Path, item)
		p.setCachedNode(resolved)
		items = append(items, resolved)
	}
	p.setCachedChildren(ctx, parent.Path, items)
	return items, nil
}

func (p *Provider) resolveNode(ctx context.Context, providerPath string) (node, error) {
	normalized := normalizePath(providerPath)
	if item, ok := p.getCachedNode(normalized); ok {
		return item, nil
	}
	if normalized == "/" {
		return p.resolveDir(ctx, "/")
	}
	if dirNode, err := p.resolveDir(ctx, normalized); err == nil {
		return dirNode, nil
	}
	item, err := p.findChild(ctx, normalized)
	if err != nil {
		return node{}, err
	}
	return item, nil
}

func (p *Provider) resolveDir(ctx context.Context, providerPath string) (node, error) {
	normalized := normalizePath(providerPath)
	if cached, ok := p.getCachedNode(normalized); ok {
		if !cached.IsDir {
			return node{}, fmt.Errorf("path %s is not a directory", normalized)
		}
		return cached, nil
	}
	root, err := p.resolveRoot(ctx)
	if err != nil {
		return node{}, err
	}
	if normalized == "/" {
		return root, nil
	}
	if p.rootPath != "/" && !hasPathPrefixFold(normalized, p.rootPath) {
		return node{}, fmt.Errorf("path %s is outside provider root %s", normalized, p.rootPath)
	}
	if strings.EqualFold(normalized, p.rootPath) {
		return root, nil
	}
	current := root
	for _, segment := range p.pathSegmentsFromRoot(normalized) {
		candidatePath := normalizePath(path.Join(current.Path, segment))
		if cached, ok := p.getCachedNode(candidatePath); ok {
			if !cached.IsDir {
				return node{}, fmt.Errorf("path %s is not a directory", normalized)
			}
			current = cached
			continue
		}
		child, err := p.findNamedChild(ctx, current, segment)
		if err != nil {
			return node{}, err
		}
		if !child.IsDir {
			return node{}, fmt.Errorf("path %s is not a directory", normalized)
		}
		current = child
	}
	p.setCachedNode(current)
	return current, nil
}

func (p *Provider) resolveRoot(ctx context.Context) (node, error) {
	if cached, ok := p.getCachedNode(p.rootPath); ok {
		return cached, nil
	}
	if p.rootPath == "/" {
		root := node{ID: "0", Path: "/", Name: "/", IsDir: true}
		p.setCachedNode(root)
		return root, nil
	}
	resp, err := p.dirNameToCID(ctx, p.rootPath)
	if err != nil {
		return node{}, err
	}
	categoryID := fmt.Sprintf("%v", resp.CategoryID)
	if categoryID == "" || categoryID == "0" {
		return node{}, fmt.Errorf("115 path not found: %s", p.rootPath)
	}
	name := path.Base(p.rootPath)
	if name == "." || name == "/" {
		name = "/"
	}
	root := node{ID: categoryID, Path: p.rootPath, Name: name, IsDir: true}
	p.setCachedNode(root)
	return root, nil
}

func (p *Provider) findChild(ctx context.Context, providerPath string) (node, error) {
	if cached, ok := p.getCachedNode(providerPath); ok {
		return cached, nil
	}
	parentPath := path.Dir(providerPath)
	if parentPath == "." {
		parentPath = "/"
	}
	baseName := path.Base(providerPath)
	parentNode, err := p.resolveDir(ctx, parentPath)
	if err != nil {
		return node{}, err
	}
	return p.findNamedChild(ctx, parentNode, baseName)
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
			p.setCachedNode(item)
			return item, nil
		}
	}
	return node{}, fmt.Errorf("115 path not found: %s (root_path=%s parent=%s real_parent=%s)", childPath, p.rootPath, parent.Path, p.realPath(parent.Path))
}

func (p *Provider) nodeFromFile(parentPath string, item pan115.File) node {
	providerPath := normalizePath(path.Join(parentPath, item.Name))
	resolved := node{
		ID:       item.FileID,
		ParentID: item.ParentID,
		Path:     providerPath,
		Name:     item.Name,
		PickCode: item.PickCode,
		IsDir:    item.IsDirectory,
		Size:     item.Size,
		ModTime:  formatTime(item.UpdateTime),
	}
	if !resolved.IsDir {
		resolved.MimeType = detectMimeType(item.Name)
	}
	return resolved
}

func (p *Provider) listFiles(ctx context.Context, dirID, providerPath string) (*[]pan115.File, error) {
	collected := make([]pan115.File, 0, listPageSize)
	offset := int64(0)
	for {
		page, err := p.listFilesPage(ctx, dirID, providerPath, offset, listPageSize)
		if err != nil {
			return nil, err
		}
		collected = append(collected, (*page)...)
		if len(*page) < listPageSize {
			break
		}
		offset += int64(len(*page))
	}
	return &collected, nil
}

func (p *Provider) getCachedNode(providerPath string) (node, bool) {
	normalized := normalizePath(providerPath)
	p.cacheMu.RLock()
	item, ok := p.nodesByPath[normalized]
	p.cacheMu.RUnlock()
	if ok {
		return item, true
	}
	return p.getPersistentCachedNode(normalized)
}

func (p *Provider) setCachedNode(item node) {
	if item.Path == "" {
		return
	}
	normalized := normalizePath(item.Path)
	p.setMemoryCachedNode(item)
	p.setPersistentCache("node:"+p.realPath(normalized), item)
}

func (p *Provider) setMemoryCachedNode(item node) {
	if item.Path == "" {
		return
	}
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	p.nodesByPath[normalizePath(item.Path)] = item
}

func (p *Provider) getPersistentCachedNode(providerPath string) (node, bool) {
	if p.cacheStore == nil {
		return node{}, false
	}
	value, ok, err := p.cacheStore.Get(context.Background(), "node:"+p.realPath(providerPath))
	if err != nil || !ok {
		return node{}, false
	}
	var item node
	if err := json.Unmarshal([]byte(value), &item); err != nil || item.Path == "" {
		return node{}, false
	}
	p.setMemoryCachedNode(item)
	return item, true
}

func (p *Provider) getCachedChildren(ctx context.Context, providerPath string) ([]node, bool) {
	if p.cacheStore == nil {
		return nil, false
	}
	value, ok, err := p.cacheStore.Get(ctx, "children:"+p.realPath(providerPath))
	if err != nil || !ok {
		return nil, false
	}
	var cached childrenCacheEntry
	if err := json.Unmarshal([]byte(value), &cached); err != nil {
		return nil, false
	}
	for _, item := range cached.Items {
		p.setMemoryCachedNode(item)
	}
	return cached.Items, true
}

func (p *Provider) setCachedChildren(ctx context.Context, providerPath string, items []node) {
	if p.cacheStore == nil {
		return
	}
	p.setPersistentCacheWithTTL(ctx, "children:"+p.realPath(providerPath), childrenCacheEntry{Items: items}, childrenCacheTTL)
}

func (p *Provider) realPath(providerPath string) string {
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

func (p *Provider) pathSegmentsFromRoot(providerPath string) []string {
	normalized := normalizePath(providerPath)
	if p.rootPath == "/" || !hasPathPrefixFold(normalized, p.rootPath) {
		return splitPathSegments(normalized)
	}
	relative := strings.TrimPrefix(normalized, p.rootPath)
	return splitPathSegments(relative)
}

func (p *Provider) setPersistentCache(key string, value any) {
	p.setPersistentCacheWithContext(context.Background(), key, value)
}

func (p *Provider) setPersistentCacheWithContext(ctx context.Context, key string, value any) {
	p.setPersistentCacheWithTTL(ctx, key, value, 0)
}

func (p *Provider) setPersistentCacheWithTTL(ctx context.Context, key string, value any, ttl time.Duration) {
	if p.cacheStore == nil {
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	if ttl > 0 {
		_ = p.cacheStore.SetWithTTL(ctx, key, string(encoded), ttl)
		return
	}
	_ = p.cacheStore.Set(ctx, key, string(encoded))
}

func (p *Provider) listFilesPage(ctx context.Context, dirID, providerPath string, offset int64, limit int) (*[]pan115.File, error) {
	var files *[]pan115.File
	var err error
	for attempt := range maxListRetries {
		if waitErr := p.waitRequest(ctx); waitErr != nil {
			return nil, waitErr
		}
		files, err = p.client.ListPage(dirID, offset, int64(limit))
		if err == nil {
			return files, nil
		}
		if !isRetryable115Error(err) || attempt == maxListRetries-1 {
			return nil, fmt.Errorf("list 115 directory %s (offset=%d limit=%d): %w", providerPath, offset, limit, err)
		}
		if sleepErr := sleepContext(ctx, time.Duration(attempt+1)*requestInterval); sleepErr != nil {
			return nil, sleepErr
		}
	}
	return nil, fmt.Errorf("list 115 directory %s (offset=%d limit=%d): %w", providerPath, offset, limit, err)
}

func (p *Provider) dirNameToCID(ctx context.Context, fullPath string) (*pan115.APIGetDirIDResp, error) {
	if err := p.waitRequest(ctx); err != nil {
		return nil, err
	}
	resp, err := p.client.DirName2CID(fullPath)
	if err != nil {
		return nil, fmt.Errorf("resolve 115 path %s: %w", fullPath, err)
	}
	return resp, nil
}

func (p *Provider) waitRequest(ctx context.Context) error {
	p.requestMu.Lock()
	defer p.requestMu.Unlock()
	if !p.lastRequest.IsZero() {
		waitFor := p.lastRequest.Add(requestInterval).Sub(time.Now())
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
	p.lastRequest = time.Now()
	return nil
}

func isRetryable115Error(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		" 405",
		"status 405",
		"too many",
		"rate limit",
		"waf",
		"频繁",
		"timeout",
		"temporary",
		"connection reset",
		"connection refused",
		"connection aborted",
		"server closed idle connection",
		"unexpected eof",
		"eof",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	if code, convErr := strconv.Atoi(strings.TrimSpace(message)); convErr == nil && code == 405 {
		return true
	}
	return false
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func splitPathSegments(value string) []string {
	trimmed := strings.Trim(normalizePath(value), "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
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

func normalizePath(value string) string {
	clean := path.Clean("/" + strings.TrimSpace(value))
	if clean == "." {
		return "/"
	}
	return clean
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func detectMimeType(name string) string {
	return mime.TypeByExtension(strings.ToLower(path.Ext(name)))
}

func headerMap(headers map[string][]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	items := make(map[string]string, len(headers))
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		items[key] = values[0]
	}
	return items
}

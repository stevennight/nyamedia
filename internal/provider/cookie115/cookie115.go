package cookie115

import (
	"context"
	"fmt"
	"mime"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	pan115 "github.com/SheltonZhu/115driver/pkg/driver"

	"emby115/internal/model"
	"emby115/internal/provider"
)

const defaultUserAgent = "Mozilla/5.0"

const (
	requestInterval = 400 * time.Millisecond
	maxListRetries  = 3
	listPageSize    = 200
)

type Provider struct {
	id        string
	rootPath  string
	userAgent string
	client    *pan115.Pan115Client

	requestMu   sync.Mutex
	lastRequest time.Time
}

type node struct {
	ID       string
	Path     string
	Name     string
	PickCode string
	IsDir    bool
	Size     int64
	ModTime  string
	MimeType string
}

func New(id, rootPath, cookieValue, userAgent string) (*Provider, error) {
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
	return &Provider{
		id:        id,
		rootPath:  normalizePath(rootPath),
		userAgent: ua,
		client:    client,
	}, nil
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
	info, err := p.client.DownloadWithUA(resolved.PickCode, p.userAgent)
	if err != nil {
		return nil, fmt.Errorf("get 115 direct link %s: %w", resolved.Path, err)
	}
	return &provider.DirectLinkResult{
		URL:           info.Url.Url,
		Headers:       headerMap(info.Header),
		SupportsRange: true,
	}, nil
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
	files, err := p.listFiles(ctx, parent.ID, parent.Path)
	if err != nil {
		return nil, err
	}
	items := make([]node, 0, len(*files))
	for _, item := range *files {
		items = append(items, p.nodeFromFile(parent.Path, item))
	}
	return items, nil
}

func (p *Provider) resolveNode(ctx context.Context, providerPath string) (node, error) {
	normalized := normalizePath(providerPath)
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
	root, err := p.resolveRoot(ctx)
	if err != nil {
		return node{}, err
	}
	if normalized == "/" {
		return root, nil
	}
	current := root
	for _, segment := range splitPathSegments(normalized) {
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
	if p.rootPath == "/" {
		return node{ID: "0", Path: "/", Name: "/", IsDir: true}, nil
	}
	resp, err := p.dirNameToCID(ctx, p.rootPath)
	if err != nil {
		return node{}, err
	}
	name := path.Base(p.rootPath)
	if name == "." || name == "/" {
		name = "/"
	}
	return node{ID: fmt.Sprintf("%v", resp.CategoryID), Path: "/", Name: name, IsDir: true}, nil
}

func (p *Provider) findChild(ctx context.Context, providerPath string) (node, error) {
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
	items, err := p.listNodesByID(ctx, parent)
	if err != nil {
		return node{}, err
	}
	for _, item := range items {
		if item.Name == name {
			return item, nil
		}
	}
	return node{}, fmt.Errorf("115 path not found: %s", normalizePath(path.Join(parent.Path, name)))
}

func (p *Provider) nodeFromFile(parentPath string, item pan115.File) node {
	providerPath := normalizePath(path.Join(parentPath, item.Name))
	resolved := node{
		ID:       item.FileID,
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
	message := strings.ToLower(err.Error())
	for _, marker := range []string{" 405", "status 405", "too many", "rate limit", "waf", "频繁"} {
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
	return provider.Entry{
		ID:       item.ID,
		Name:     item.Name,
		Path:     item.Path,
		IsDir:    item.IsDir,
		Size:     item.Size,
		ModTime:  item.ModTime,
		MimeType: item.MimeType,
	}
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

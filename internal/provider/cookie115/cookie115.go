package cookie115

import (
	"context"
	"fmt"
	"mime"
	"path"
	"strings"
	"time"

	pan115 "github.com/SheltonZhu/115driver/pkg/driver"

	"emby115/internal/model"
	"emby115/internal/provider"
)

const defaultUserAgent = "Mozilla/5.0"

type Provider struct {
	id        string
	rootPath  string
	userAgent string
	client    *pan115.Pan115Client
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
	_ = ctx
	dirNode, err := p.resolveDir(providerPath)
	if err != nil {
		return nil, err
	}
	files, err := p.client.ListWithLimit(dirNode.ID, 1000)
	if err != nil {
		return nil, fmt.Errorf("list 115 directory %s: %w", dirNode.Path, err)
	}
	items := make([]provider.Entry, 0, len(*files))
	for _, item := range *files {
		entry := p.entryFromFile(dirNode.Path, item)
		items = append(items, entry)
	}
	return items, nil
}

func (p *Provider) Stat(ctx context.Context, providerPath string) (*provider.Entry, error) {
	_ = ctx
	resolved, err := p.resolveNode(providerPath)
	if err != nil {
		return nil, err
	}
	entry := toEntry(resolved)
	return &entry, nil
}

func (p *Provider) GetDirectLink(ctx context.Context, providerPath string) (*provider.DirectLinkResult, error) {
	_ = ctx
	resolved, err := p.resolveNode(providerPath)
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
	_ = ctx
	if err := p.client.CookieCheck(); err != nil {
		return model.ProviderStatusError, err.Error()
	}
	if _, err := p.resolveDir("/"); err != nil {
		return model.ProviderStatusError, err.Error()
	}
	return model.ProviderStatusHealthy, ""
}

func (p *Provider) WalkFiles(ctx context.Context, sourcePath string, fn func(entry provider.Entry) error) error {
	root, err := p.resolveDir(sourcePath)
	if err != nil {
		return err
	}
	return p.walkNode(ctx, root, fn)
}

func (p *Provider) walkNode(ctx context.Context, current node, fn func(entry provider.Entry) error) error {
	items, err := p.listNodesByID(current)
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

func (p *Provider) listNodesByID(parent node) ([]node, error) {
	files, err := p.client.ListWithLimit(parent.ID, 1000)
	if err != nil {
		return nil, fmt.Errorf("list 115 directory %s: %w", parent.Path, err)
	}
	items := make([]node, 0, len(*files))
	for _, item := range *files {
		items = append(items, p.nodeFromFile(parent.Path, item))
	}
	return items, nil
}

func (p *Provider) resolveNode(providerPath string) (node, error) {
	normalized := normalizePath(providerPath)
	if normalized == "/" {
		return p.resolveDir("/")
	}
	if dirNode, err := p.resolveDir(normalized); err == nil {
		return dirNode, nil
	}
	item, err := p.findChild(normalized)
	if err != nil {
		return node{}, err
	}
	return item, nil
}

func (p *Provider) resolveDir(providerPath string) (node, error) {
	normalized := normalizePath(providerPath)
	fullPath := p.fullPath(normalized)
	if fullPath == "/" {
		return node{ID: "0", Path: "/", Name: "/", IsDir: true}, nil
	}
	resp, err := p.client.DirName2CID(fullPath)
	if err != nil {
		return node{}, fmt.Errorf("resolve 115 path %s: %w", fullPath, err)
	}
	name := path.Base(normalized)
	if normalized == "/" {
		name = "/"
	}
	return node{ID: fmt.Sprintf("%v", resp.CategoryID), Path: normalized, Name: name, IsDir: true}, nil
}

func (p *Provider) findChild(providerPath string) (node, error) {
	parentPath := path.Dir(providerPath)
	if parentPath == "." {
		parentPath = "/"
	}
	baseName := path.Base(providerPath)
	parentNode, err := p.resolveDir(parentPath)
	if err != nil {
		return node{}, err
	}
	files, err := p.client.ListWithLimit(parentNode.ID, 1000)
	if err != nil {
		return node{}, fmt.Errorf("list 115 directory %s: %w", parentNode.Path, err)
	}
	for _, item := range *files {
		if item.Name == baseName {
			return p.nodeFromFile(parentPath, item), nil
		}
	}
	return node{}, fmt.Errorf("115 path not found: %s", providerPath)
}

func (p *Provider) entryFromFile(parentPath string, item pan115.File) provider.Entry {
	return toEntry(p.nodeFromFile(parentPath, item))
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

func (p *Provider) fullPath(providerPath string) string {
	normalized := normalizePath(providerPath)
	if p.rootPath == "/" {
		return normalized
	}
	if normalized == "/" {
		return p.rootPath
	}
	return normalizePath(path.Join(p.rootPath, normalized))
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

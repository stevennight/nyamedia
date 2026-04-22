package local

import (
	"context"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"emby115/internal/provider"
)

type Provider struct {
	id       string
	rootPath string
}

func New(id, rootPath string) *Provider {
	return &Provider{id: id, rootPath: filepath.Clean(rootPath)}
}

func (p *Provider) ID() string {
	return p.id
}

func (p *Provider) Type() string {
	return "local"
}

func (p *Provider) List(ctx context.Context, providerPath string) ([]provider.Entry, error) {
	absPath, err := p.resolve(providerPath)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", absPath, err)
	}

	items := make([]provider.Entry, 0, len(entries))
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat entry %s: %w", entry.Name(), err)
		}

		childPath := path.Join(cleanProviderPath(providerPath), entry.Name())
		if !strings.HasPrefix(childPath, "/") {
			childPath = "/" + childPath
		}

		items = append(items, fromFileInfo(childPath, info))
	}

	return items, nil
}

func (p *Provider) Stat(ctx context.Context, providerPath string) (*provider.Entry, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	absPath, err := p.resolve(providerPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat path %s: %w", absPath, err)
	}
	entry := fromFileInfo(cleanProviderPath(providerPath), info)
	return &entry, nil
}

func (p *Provider) GetDirectLink(context.Context, string) (*provider.DirectLinkResult, error) {
	return nil, fmt.Errorf("local provider does not use direct links")
}

func (p *Provider) ResolveFilePath(providerPath string) (string, error) {
	return p.resolve(providerPath)
}

func (p *Provider) WalkFiles(ctx context.Context, sourcePath string, fn func(entry provider.Entry) error) error {
	basePath, err := p.resolve(sourcePath)
	if err != nil {
		return err
	}

	return filepath.WalkDir(basePath, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat file %s: %w", current, err)
		}

		rel, err := filepath.Rel(p.rootPath, current)
		if err != nil {
			return fmt.Errorf("resolve relative path %s: %w", current, err)
		}

		providerPath := "/" + filepath.ToSlash(rel)
		return fn(fromFileInfo(providerPath, info))
	})
}

func (p *Provider) resolve(providerPath string) (string, error) {
	clean := strings.TrimPrefix(cleanProviderPath(providerPath), "/")
	resolved := filepath.Clean(filepath.Join(p.rootPath, filepath.FromSlash(clean)))
	if resolved != p.rootPath && !strings.HasPrefix(resolved, p.rootPath+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes provider root")
	}
	return resolved, nil
}

func cleanProviderPath(value string) string {
	if value == "" {
		return "/"
	}
	clean := path.Clean("/" + strings.TrimSpace(value))
	if clean == "." {
		return "/"
	}
	return clean
}

func fromFileInfo(providerPath string, info os.FileInfo) provider.Entry {
	mimeType := mime.TypeByExtension(filepath.Ext(info.Name()))
	return provider.Entry{
		ID:       providerPath,
		Name:     info.Name(),
		Path:     providerPath,
		IsDir:    info.IsDir(),
		Size:     info.Size(),
		ModTime:  info.ModTime().UTC().Format(time.RFC3339),
		MimeType: mimeType,
	}
}

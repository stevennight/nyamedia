package local

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"NyaMedia/internal/model"
	"NyaMedia/internal/provider"
	"github.com/fsnotify/fsnotify"
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

func (p *Provider) GetDirectLinkForEntry(context.Context, provider.DirectLinkInput) (*provider.DirectLinkResult, error) {
	return nil, fmt.Errorf("local provider does not use direct links")
}

func (p *Provider) CheckStatus(ctx context.Context) (model.ProviderStatus, string) {
	select {
	case <-ctx.Done():
		return model.ProviderStatusError, ctx.Err().Error()
	default:
	}

	info, err := os.Stat(p.rootPath)
	if err != nil {
		return model.ProviderStatusError, fmt.Sprintf("stat root path %s: %v", p.rootPath, err)
	}
	if !info.IsDir() {
		return model.ProviderStatusError, fmt.Sprintf("root path is not a directory: %s", p.rootPath)
	}

	return model.ProviderStatusHealthy, ""
}

func (p *Provider) ResolveFilePath(providerPath string) (string, error) {
	return p.resolve(providerPath)
}

func (p *Provider) WalkFiles(ctx context.Context, sourcePath string, options provider.WalkOptions, fn func(entry provider.Entry) error) error {
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
			ignored, err := localDirHasIgnoreFile(current)
			if err != nil {
				return err
			}
			if ignored {
				providerPath, err := p.providerPathFromAbsolute(current)
				if err != nil {
					return err
				}
				if options.OnIgnoredDir != nil {
					if err := options.OnIgnoredDir(providerPath); err != nil {
						return err
					}
				}
				return filepath.SkipDir
			}
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

func localDirHasIgnoreFile(dirPath string) (bool, error) {
	info, err := os.Stat(filepath.Join(dirPath, provider.IgnoreFileName))
	if err == nil {
		return !info.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("stat ignore file in %s: %w", dirPath, err)
}

func (p *Provider) Watch(ctx context.Context, sourcePath string, emit func(provider.ChangeEvent)) error {
	basePath, err := p.resolve(sourcePath)
	if err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create fs watcher: %w", err)
	}
	defer watcher.Close()

	if err := p.addRecursiveWatches(watcher, basePath); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			if err != nil && err != io.EOF {
				return fmt.Errorf("watch local provider %s: %w", p.id, err)
			}
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Op&fsnotify.Chmod != 0 {
				continue
			}

			providerPath, err := p.providerPathFromAbsolute(event.Name)
			if err != nil {
				continue
			}

			isDir := false
			if info, statErr := os.Stat(event.Name); statErr == nil {
				isDir = info.IsDir()
				if isDir && event.Op&fsnotify.Create != 0 {
					if err := p.addRecursiveWatches(watcher, event.Name); err != nil {
						return err
					}
				}
			}

			if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				_ = watcher.Remove(event.Name)
			}

			changeType, ok := mapChangeType(event.Op)
			if !ok {
				continue
			}

			emit(provider.ChangeEvent{
				ProviderID: p.id,
				Path:       providerPath,
				Type:       changeType,
				IsDir:      isDir,
			})
		}
	}
}

func (p *Provider) resolve(providerPath string) (string, error) {
	clean := strings.TrimPrefix(cleanProviderPath(providerPath), "/")
	resolved := filepath.Clean(filepath.Join(p.rootPath, filepath.FromSlash(clean)))
	if resolved != p.rootPath && !strings.HasPrefix(resolved, p.rootPath+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes provider root")
	}
	return resolved, nil
}

func (p *Provider) providerPathFromAbsolute(absPath string) (string, error) {
	clean := filepath.Clean(absPath)
	if clean != p.rootPath && !strings.HasPrefix(clean, p.rootPath+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes provider root")
	}
	rel, err := filepath.Rel(p.rootPath, clean)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return "/", nil
	}
	return "/" + filepath.ToSlash(rel), nil
}

func (p *Provider) addRecursiveWatches(watcher *fsnotify.Watcher, basePath string) error {
	return filepath.WalkDir(basePath, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		if err := watcher.Add(current); err != nil {
			return fmt.Errorf("watch dir %s: %w", current, err)
		}
		return nil
	})
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

func mapChangeType(op fsnotify.Op) (provider.ChangeType, bool) {
	switch {
	case op&fsnotify.Create != 0:
		return provider.ChangeTypeCreate, true
	case op&fsnotify.Write != 0:
		return provider.ChangeTypeWrite, true
	case op&fsnotify.Remove != 0:
		return provider.ChangeTypeRemove, true
	case op&fsnotify.Rename != 0:
		return provider.ChangeTypeRename, true
	default:
		return "", false
	}
}

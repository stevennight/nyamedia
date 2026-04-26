package provider

import (
	"context"
	"strings"

	"NyaMedia/internal/model"
)

type requestUserAgentContextKey struct{}

type Entry struct {
	ID       string
	Name     string
	Path     string
	IsDir    bool
	Size     int64
	ModTime  string
	MimeType string
	Metadata map[string]string
}

type DirectLinkResult struct {
	URL           string
	Headers       map[string]string
	ExpireAt      string
	SupportsRange bool
}

type DirectLinkInput struct {
	Path            string
	ProviderEntryID string
	Metadata        map[string]string
}

func WithRequestUserAgent(ctx context.Context, userAgent string) context.Context {
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" {
		return ctx
	}
	return context.WithValue(ctx, requestUserAgentContextKey{}, userAgent)
}

func RequestUserAgentFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(requestUserAgentContextKey{}).(string)
	return strings.TrimSpace(value)
}

type ChangeType string

const (
	ChangeTypeCreate ChangeType = "create"
	ChangeTypeWrite  ChangeType = "write"
	ChangeTypeRemove ChangeType = "remove"
	ChangeTypeRename ChangeType = "rename"
)

type ChangeEvent struct {
	ProviderID string
	Path       string
	Type       ChangeType
	IsDir      bool
}

type Provider interface {
	ID() string
	Type() string
	List(ctx context.Context, path string) ([]Entry, error)
	Stat(ctx context.Context, path string) (*Entry, error)
	GetDirectLinkForEntry(ctx context.Context, input DirectLinkInput) (*DirectLinkResult, error)
}

type PersistedEntryMetadataProvider interface {
	LoadPersistedEntryMetadata(providerPath string, providerEntryID string, metadata map[string]string)
}

type StatusProvider interface {
	CheckStatus(ctx context.Context) (model.ProviderStatus, string)
}

type ScanProvider interface {
	WalkFiles(ctx context.Context, sourcePath string, fn func(entry Entry) error) error
}

type LocalFileProvider interface {
	ResolveFilePath(providerPath string) (string, error)
}

type WatchProvider interface {
	Watch(ctx context.Context, path string, emit func(ChangeEvent)) error
}

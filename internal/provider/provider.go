package provider

import "context"

type Entry struct {
	ID       string
	Name     string
	Path     string
	IsDir    bool
	Size     int64
	ModTime  string
	MimeType string
}

type DirectLinkResult struct {
	URL           string
	Headers       map[string]string
	ExpireAt      string
	SupportsRange bool
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
	GetDirectLink(ctx context.Context, path string) (*DirectLinkResult, error)
}

type WatchProvider interface {
	Watch(ctx context.Context, path string, emit func(ChangeEvent)) error
}

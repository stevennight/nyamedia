package cookie115

import (
	"context"
	"errors"
	"testing"
)

type retryableNetError struct{}

func (retryableNetError) Error() string   { return "net error" }
func (retryableNetError) Timeout() bool   { return true }
func (retryableNetError) Temporary() bool { return true }

func TestIsRetryable115Error(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "tls handshake timeout", err: errors.New(`Get "https://webapi.115.com/files": net/http: TLS handshake timeout`), want: true},
		{name: "net timeout", err: retryableNetError{}, want: true},
		{name: "rate limit", err: errors.New("status 405 too many requests"), want: true},
		{name: "context canceled", err: context.Canceled, want: false},
		{name: "regular error", err: errors.New("bad request"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryable115Error(tt.err); got != tt.want {
				t.Fatalf("isRetryable115Error() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRootPrefixedPathsStayAbsolute(t *testing.T) {
	p := &Provider{rootPath: "/Video"}

	segments := p.pathSegmentsFromRoot("/Video/TV/Anime")
	if len(segments) != 2 || segments[0] != "TV" || segments[1] != "Anime" {
		t.Fatalf("segments = %#v, want [TV Anime]", segments)
	}

	if got := p.realPath("/Video/TV/Anime"); got != "/Video/TV/Anime" {
		t.Fatalf("realPath(root-prefixed) = %q, want /Video/TV/Anime", got)
	}
}

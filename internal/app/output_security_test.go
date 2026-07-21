package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"NyaMedia/internal/config"
	"NyaMedia/internal/model"
	provideriface "NyaMedia/internal/provider"
)

type directLinkTestProvider struct {
	directLink *provideriface.DirectLinkResult
	err        error
	calls      int
	deadline   time.Time
}

func (p *directLinkTestProvider) ID() string   { return "test" }
func (p *directLinkTestProvider) Type() string { return "test" }
func (p *directLinkTestProvider) List(context.Context, string) ([]provideriface.Entry, error) {
	return nil, nil
}
func (p *directLinkTestProvider) Stat(context.Context, string) (*provideriface.Entry, error) {
	return nil, nil
}
func (p *directLinkTestProvider) GetDirectLinkForEntry(ctx context.Context, _ provideriface.DirectLinkInput) (*provideriface.DirectLinkResult, error) {
	p.calls++
	p.deadline, _ = ctx.Deadline()
	return p.directLink, p.err
}

func TestToLibraryMountModelNormalizesAndValidatesTargetPath(t *testing.T) {
	payload := libraryMountPayload{
		ID:         "mount-1",
		ProviderID: "provider-1",
		SourcePath: "/Video",
		TargetPath: ` /TV\Shows/./Season 01 `,
	}
	mount, err := toLibraryMountModel("library-1", payload)
	if err != nil {
		t.Fatalf("toLibraryMountModel() error = %v", err)
	}
	if mount.TargetPath != "/TV/Shows/Season 01" {
		t.Fatalf("TargetPath = %q, want normalized virtual path", mount.TargetPath)
	}

	for _, targetPath := range []string{"", "   ", "../outside", `/TV\..\outside`, "/TV/../outside", "/TV/.. /outside", "C:/outside"} {
		payload.TargetPath = targetPath
		if mount, err := toLibraryMountModel("library-1", payload); err == nil {
			t.Fatalf("target_path %q accepted as %q", targetPath, mount.TargetPath)
		}
	}
}

func TestMountOutputRootRejectsLegacyTraversal(t *testing.T) {
	outputRoot := t.TempDir()
	a := &App{config: config.Config{Storage: config.StorageConfig{STRMOutputDir: outputRoot}}}

	for _, targetPath := range []string{"../outside", `/TV\..\outside`, "/TV/../../outside", "C:/outside"} {
		mount := model.LibraryMount{ID: "legacy", SourcePath: "/Video", TargetPath: targetPath}
		if targetRoot, err := a.mountOutputRoot(mount); err == nil {
			t.Fatalf("legacy target_path %q resolved to %q", targetPath, targetRoot)
		}
	}
}

func TestOutputDirectoryPathRejectsTraversal(t *testing.T) {
	outputRoot := t.TempDir()
	a := &App{config: config.Config{Storage: config.StorageConfig{STRMOutputDir: outputRoot}}}

	if outputPath, err := a.outputDirectoryPath(""); err != nil || outputPath != outputRoot {
		t.Fatalf("root output path = %q, err = %v", outputPath, err)
	}
	for _, virtualPath := range []string{"../outside", `/TV\..\outside`, "/TV/.. /outside"} {
		if outputPath, err := a.outputDirectoryPath(virtualPath); err == nil {
			t.Fatalf("output path %q resolved to %q", virtualPath, outputPath)
		}
	}
}

func TestEnsureOutputPathWithinRootRejectsSymbolicLinkEscape(t *testing.T) {
	outputRoot := t.TempDir()
	outside := t.TempDir()
	linkPath := filepath.Join(outputRoot, "outside")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	if err := ensureOutputPathWithinRoot(outputRoot, filepath.Join(linkPath, "poster.jpg")); err == nil {
		t.Fatal("symbolic-link output escape was accepted")
	}
}

func TestProviderOutputPathsRejectWindowsSeparatorTraversal(t *testing.T) {
	outputRoot := t.TempDir()
	a := &App{config: config.Config{Storage: config.StorageConfig{STRMOutputDir: outputRoot}}}
	mount := model.LibraryMount{ID: "mount-a", SourcePath: "/", TargetPath: "/TV"}

	if _, err := a.mediaOutputPaths(mount, `/Show\..\Other/movie.mkv`); err == nil {
		t.Fatal("media output accepted a provider path containing backslash traversal")
	}
	if _, ok := classifyDirectoryOutputSyncJob(provideriface.Entry{
		Name: `poster.jpg\..\outside.jpg`,
		Path: "/poster.jpg",
	}, filepath.Join(outputRoot, "TV"), providerDownloadOptions{Images: true}); ok {
		t.Fatal("directory output job accepted a provider name containing separators")
	}
}

func TestDownloadProviderFileRejectsSymlinkToSiblingMount(t *testing.T) {
	outputRoot := t.TempDir()
	mountA := filepath.Join(outputRoot, "mount-a")
	mountB := filepath.Join(outputRoot, "mount-b")
	if err := os.MkdirAll(mountA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mountB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "mount-b"), filepath.Join(mountA, "sibling")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	provider := &directLinkTestProvider{err: fmt.Errorf("should not be called")}
	a := &App{config: config.Config{Storage: config.StorageConfig{STRMOutputDir: outputRoot}}}
	targetPath := filepath.Join(mountA, "sibling", "poster.jpg")
	if err := a.downloadProviderFile(context.Background(), provider, mountA, "/poster.jpg", targetPath, nil, nil); err == nil {
		t.Fatal("download accepted a symlink into a sibling mount")
	}
	if provider.calls != 0 {
		t.Fatalf("direct link calls = %d, want 0", provider.calls)
	}
}

func TestRemoveRootedOutputTreeRemovesSymlinkOnly(t *testing.T) {
	outputRoot := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(outsideFile, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(outputRoot, "outside-link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	if err := removeRootedOutputTree(outputRoot, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outsideFile); err != nil {
		t.Fatalf("outside file was removed: %v", err)
	}
	if _, err := os.Lstat(linkPath); !os.IsNotExist(err) {
		t.Fatalf("output symlink still exists or unexpected error: %v", err)
	}
}

func TestDownloadProviderFileKeepsSignedURLAndHeadersOutOfProgress(t *testing.T) {
	const headerSecret = "Bearer header-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != headerSecret {
			t.Errorf("Authorization header = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte("subtitle"))
	}))
	defer server.Close()

	provider := &directLinkTestProvider{directLink: &provideriface.DirectLinkResult{
		URL:     server.URL + "/subtitle.srt?token=query-secret&signature=signed-value",
		Headers: map[string]string{"Authorization": headerSecret},
	}}
	outputRoot := t.TempDir()
	targetPath := filepath.Join(outputRoot, "TV", "subtitle.srt")
	a := &App{config: config.Config{Storage: config.StorageConfig{STRMOutputDir: outputRoot}}}

	type progressEntry struct {
		Message string
		Payload map[string]any
	}
	entries := make([]progressEntry, 0)
	err := a.downloadProviderFile(context.Background(), provider, outputRoot, "/subtitle.srt", targetPath, nil, func(message string, payload map[string]any) {
		entries = append(entries, progressEntry{Message: message, Payload: payload})
	})
	if err != nil {
		t.Fatalf("downloadProviderFile() error = %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("direct link calls = %d, want 1", provider.calls)
	}
	if !provider.deadline.IsZero() {
		t.Fatalf("direct link provider received a workflow deadline: %s", provider.deadline)
	}

	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	logged := string(encoded)
	for _, secret := range []string{server.URL, "query-secret", "signed-value", headerSecret, "direct_link_url", "request_url"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("progress contains sensitive value %q: %s", secret, logged)
		}
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "subtitle" {
		t.Fatalf("downloaded data = %q", data)
	}
}

func TestDownloadProviderFileRejectsTargetOutsideOutputRoot(t *testing.T) {
	outputRoot := t.TempDir()
	provider := &directLinkTestProvider{err: fmt.Errorf("should not be called")}
	a := &App{config: config.Config{Storage: config.StorageConfig{STRMOutputDir: outputRoot}}}
	targetPath := filepath.Join(outputRoot, "..", "outside.nfo")

	if err := a.downloadProviderFile(context.Background(), provider, outputRoot, "/outside.nfo", targetPath, nil, nil); err == nil {
		t.Fatal("outside download target was accepted")
	}
	if provider.calls != 0 {
		t.Fatalf("direct link calls = %d, want 0", provider.calls)
	}
}

func TestProviderDownloadHTTPClientTimeouts(t *testing.T) {
	client := newProviderDownloadHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if client.Timeout != 0 {
		t.Fatalf("client timeout = %s, file request timeout must come from its request context", client.Timeout)
	}
	if transport.ResponseHeaderTimeout != providerDownloadHeaderTimeout {
		t.Fatalf("response header timeout = %s", transport.ResponseHeaderTimeout)
	}
	if transport.TLSHandshakeTimeout != providerDownloadTLSHandshakeTimeout {
		t.Fatalf("TLS handshake timeout = %s", transport.TLSHandshakeTimeout)
	}
	if providerDownloadRequestTimeout != 2*time.Minute {
		t.Fatalf("download request timeout = %s, want 2m", providerDownloadRequestTimeout)
	}
}

func TestRedactURLsForLog(t *testing.T) {
	input := `download failed for https://cdn.example.test/subtitle.srt?token=secret&signature=value: timeout`
	got := redactURLsForLog(input)
	if strings.Contains(got, "secret") || strings.Contains(got, "https://") {
		t.Fatalf("redacted log = %q", got)
	}
}

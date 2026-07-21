package local

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveFilePathAllowsExistingAndMissingPathsWithinRoot(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "Show", "episode.nfo")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	provider := New("local", root)
	resolved, err := provider.ResolveFilePath("/Show/episode.nfo")
	if err != nil {
		t.Fatalf("resolve existing path: %v", err)
	}
	wantExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != wantExisting {
		t.Fatalf("resolved existing path = %q, want %q", resolved, wantExisting)
	}

	resolved, err = provider.ResolveFilePath("/Show/new-subtitle.srt")
	if err != nil {
		t.Fatalf("resolve missing path: %v", err)
	}
	wantMissing := filepath.Join(filepath.Dir(wantExisting), "new-subtitle.srt")
	if resolved != wantMissing {
		t.Fatalf("resolved missing path = %q, want %q", resolved, wantMissing)
	}
}

func TestResolveFilePathRejectsSymbolicLinkOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(root, "outside")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	provider := New("local", root)
	for _, providerPath := range []string{"/outside/secret.txt", "/outside/new-file.txt"} {
		if resolved, err := provider.ResolveFilePath(providerPath); err == nil {
			t.Fatalf("ResolveFilePath(%q) = %q, want symbolic-link escape error", providerPath, resolved)
		}
	}
}

func TestResolveFilePathAllowsSymbolicLinkWithinRoot(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "target")
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(root, "link")
	if err := os.Symlink(targetDir, linkPath); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	provider := New("local", root)
	resolved, err := provider.ResolveFilePath("/link/new-file.txt")
	if err != nil {
		t.Fatalf("resolve in-root symbolic link: %v", err)
	}
	want := filepath.Join(targetDir, "new-file.txt")
	if resolved != want {
		t.Fatalf("resolved path = %q, want %q", resolved, want)
	}
}

func TestOpenFileRejectsSymbolicLinkOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	provider := New("local", root)
	if file, err := provider.OpenFile("/outside/secret.txt"); err == nil {
		file.Close()
		t.Fatal("OpenFile accepted a symbolic link outside the provider root")
	}
}

func TestOpenFileAllowsRelativeSymbolicLinkWithinRoot(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "target")
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "file.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(root, "link")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	provider := New("local", root)
	file, err := provider.OpenFile("/link/file.txt")
	if err != nil {
		t.Fatalf("OpenFile rejected a relative in-root symbolic link: %v", err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "inside" {
		t.Fatalf("file data = %q", data)
	}
}

func TestOpenFileRejectsAbsoluteSymbolicLinkWithinRoot(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "absolute-link.txt")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	provider := New("local", root)
	if file, err := provider.OpenFile("/absolute-link.txt"); err == nil {
		file.Close()
		t.Fatal("OpenFile accepted an absolute symbolic link")
	}
}

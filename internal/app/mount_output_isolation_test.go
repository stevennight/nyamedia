package app

import (
	"context"
	"database/sql"
	"testing"

	"NyaMedia/internal/config"
	"NyaMedia/internal/model"
	"NyaMedia/internal/storage"

	_ "modernc.org/sqlite"
)

func TestValidateMountOutputIsolationRejectsOverlappingTarget(t *testing.T) {
	a, db := newMountOutputTestApp(t)
	insertMountOutputFixture(t, db, "mount-a", "library-a", "/TV")

	candidate := model.LibraryMount{ID: "mount-b", LibraryID: "library-b", TargetPath: "/TV/Shows"}
	if err := a.validateMountOutputIsolation(context.Background(), candidate); err == nil {
		t.Fatal("nested mount target was accepted")
	}
	candidate.TargetPath = "/Movies"
	if err := a.validateMountOutputIsolation(context.Background(), candidate); err != nil {
		t.Fatalf("independent mount target was rejected: %v", err)
	}
}

func TestValidateCleanupMountOutputDirsRejectsSharedTarget(t *testing.T) {
	a, db := newMountOutputTestApp(t)
	insertMountOutputFixture(t, db, "mount-a", "library-a", "/TV")
	insertMountOutputFixture(t, db, "mount-b", "library-b", "/TV/Shows")

	mount := model.LibraryMount{ID: "mount-a", LibraryID: "library-a", TargetPath: "/TV"}
	if err := a.validateCleanupMountOutputDirs(context.Background(), []model.LibraryMount{mount}); err == nil {
		t.Fatal("cleanup of a target shared with another mount was accepted")
	}
}

func newMountOutputTestApp(t *testing.T) (*App, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	const schema = `
CREATE TABLE libraries (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    enabled INTEGER NOT NULL,
    last_scan_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE library_mounts (
    id TEXT PRIMARY KEY,
    library_id TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    source_path TEXT NOT NULL,
    target_path TEXT NOT NULL,
    media_type TEXT,
    priority INTEGER NOT NULL,
    enabled INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	for _, libraryID := range []string{"library-a", "library-b"} {
		if _, err := db.Exec(`INSERT INTO libraries (id, name, enabled, created_at, updated_at) VALUES (?, ?, 1, 'now', 'now')`, libraryID, libraryID); err != nil {
			t.Fatal(err)
		}
	}
	return &App{
		config:    config.Config{Storage: config.StorageConfig{STRMOutputDir: t.TempDir()}},
		libraries: storage.NewLibraryRepository(db),
	}, db
}

func insertMountOutputFixture(t *testing.T, db *sql.DB, mountID, libraryID, targetPath string) {
	t.Helper()
	if _, err := db.Exec(`
INSERT INTO library_mounts (id, library_id, provider_id, source_path, target_path, priority, enabled, created_at, updated_at)
VALUES (?, ?, ?, '/', ?, 100, 1, 'now', 'now')`, mountID, libraryID, "provider-"+mountID, targetPath); err != nil {
		t.Fatal(err)
	}
}

package app

import (
	"testing"

	"NyaMedia/internal/model"
)

func TestProviderScanLockSerializesSameProvider(t *testing.T) {
	app := &App{activeProviders: make(map[string]struct{})}

	if !app.tryLockProvider("provider-a") {
		t.Fatal("first lock for provider-a was rejected")
	}
	if app.tryLockProvider("provider-a") {
		t.Fatal("second lock for provider-a succeeded while the first was held")
	}
	if !app.tryLockProvider("provider-b") {
		t.Fatal("independent provider-b lock was rejected")
	}

	app.unlockProvider("provider-a")
	if !app.tryLockProvider("provider-a") {
		t.Fatal("provider-a could not be locked after release")
	}

	app.unlockProvider("provider-a")
	app.unlockProvider("provider-b")
}

func TestLibraryScanConcurrencyLimit(t *testing.T) {
	if maxConcurrentLibraryScans != 2 {
		t.Fatalf("maxConcurrentLibraryScans = %d; want 2", maxConcurrentLibraryScans)
	}
}

func TestFindMountForScanSourcePathHonorsQueuedMountID(t *testing.T) {
	mounts := []model.LibraryMount{
		{ID: "mount-a", ProviderID: "provider-a", SourcePath: "/"},
		{ID: "mount-b", ProviderID: "provider-b", SourcePath: "/"},
	}

	mount, ok := findMountForScanSourcePath(mounts, "mount-b", "/Shows")
	if !ok {
		t.Fatal("queued mount-b was not selected")
	}
	if mount.ID != "mount-b" || mount.ProviderID != "provider-b" {
		t.Fatalf("selected mount = %+v; want mount-b/provider-b", mount)
	}
}

func TestFindMountForTargetPathPreservesMountIdentity(t *testing.T) {
	mounts := []model.LibraryMount{
		{ID: "mount-a", ProviderID: "provider-a", SourcePath: "/", TargetPath: "/Movies"},
		{ID: "mount-b", ProviderID: "provider-b", SourcePath: "/", TargetPath: "/Shows"},
	}

	mount, ok := findMountForTargetPath(mounts, "/Shows/Series")
	if !ok || mount.ID != "mount-b" || mount.ProviderID != "provider-b" {
		t.Fatalf("selected mount = %+v, ok = %t; want mount-b/provider-b", mount, ok)
	}
}

func TestValidateMountProviderRejectsQueueSnapshotMismatch(t *testing.T) {
	mount := model.LibraryMount{ID: "mount-a", ProviderID: "provider-b"}
	if err := validateMountProvider(mount, "provider-a"); err == nil {
		t.Fatal("provider change after dequeue was accepted")
	}
}

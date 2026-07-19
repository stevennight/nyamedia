package app

import (
	"testing"

	"NyaMedia/internal/model"
)

func TestResolveScanScheduleTargets(t *testing.T) {
	mounts := []model.LibraryMount{
		{ID: "anime", LibraryID: "tv", ProviderID: "provider-a", SourcePath: "/Video/TV/Anime", Enabled: true},
		{ID: "drama", LibraryID: "tv", ProviderID: "provider-a", SourcePath: "/Video/TV/Drama/", Enabled: true},
	}

	tests := []struct {
		name        string
		schedule    model.ScanSchedule
		wantMounts  []string
		wantSources []string
		wantErr     bool
	}{
		{
			name:        "legacy whole library targets every enabled mount",
			schedule:    model.ScanSchedule{LibraryID: "tv"},
			wantMounts:  []string{"anime", "drama"},
			wantSources: []string{"/Video/TV/Anime", "/Video/TV/Drama"},
		},
		{
			name:        "specific directory targets one mount",
			schedule:    model.ScanSchedule{LibraryID: "tv", MountID: "anime", SourcePath: "/Video/TV/Anime/Active Show/Season 01/"},
			wantMounts:  []string{"anime"},
			wantSources: []string{"/Video/TV/Anime/Active Show/Season 01"},
		},
		{
			name:        "empty source targets selected mount root",
			schedule:    model.ScanSchedule{LibraryID: "tv", MountID: "anime"},
			wantMounts:  []string{"anime"},
			wantSources: []string{"/Video/TV/Anime"},
		},
		{
			name:     "source without mount is rejected",
			schedule: model.ScanSchedule{LibraryID: "tv", SourcePath: "/Video/TV/Anime"},
			wantErr:  true,
		},
		{
			name:     "directory outside mount is rejected",
			schedule: model.ScanSchedule{LibraryID: "tv", MountID: "anime", SourcePath: "/Video/TV/Drama"},
			wantErr:  true,
		},
		{
			name:     "partial segment prefix is rejected",
			schedule: model.ScanSchedule{LibraryID: "tv", MountID: "anime", SourcePath: "/Video/TV/Anime2"},
			wantErr:  true,
		},
		{
			name:     "unknown mount is rejected",
			schedule: model.ScanSchedule{LibraryID: "tv", MountID: "missing", SourcePath: "/Video/TV/Anime"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targets, err := resolveScanScheduleTargets(tt.schedule, mounts)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveScanScheduleTargets() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(targets) != len(tt.wantMounts) {
				t.Fatalf("len(targets) = %d, want %d", len(targets), len(tt.wantMounts))
			}
			for i, target := range targets {
				if target.Mount.ID != tt.wantMounts[i] {
					t.Errorf("targets[%d].Mount.ID = %q, want %q", i, target.Mount.ID, tt.wantMounts[i])
				}
				if target.SourcePath != tt.wantSources[i] {
					t.Errorf("targets[%d].SourcePath = %q, want %q", i, target.SourcePath, tt.wantSources[i])
				}
			}
		})
	}
}

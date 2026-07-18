package app

import "testing"

func TestToScanScheduleModel(t *testing.T) {
	item, err := toScanScheduleModel(scanSchedulePayload{
		Name:       " Active anime ",
		LibraryID:  " anime ",
		MountID:    " anime-main ",
		SourcePath: " active/season-1/../season-2 ",
		Cron:       " 0 */2 * * * ",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("toScanScheduleModel() error = %v", err)
	}
	if item.Name != "Active anime" || item.LibraryID != "anime" || item.MountID != "anime-main" {
		t.Fatalf("toScanScheduleModel() identity fields = %+v", item)
	}
	if item.SourcePath != "/active/season-2" {
		t.Fatalf("SourcePath = %q, want %q", item.SourcePath, "/active/season-2")
	}
	if item.Cron != "0 */2 * * *" || !item.Enabled {
		t.Fatalf("toScanScheduleModel() schedule fields = %+v", item)
	}
}

func TestToScanScheduleModelRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		payload scanSchedulePayload
	}{
		{name: "missing name", payload: scanSchedulePayload{LibraryID: "anime", Cron: "0 4 * * *"}},
		{name: "missing library", payload: scanSchedulePayload{Name: "Anime", Cron: "0 4 * * *"}},
		{name: "missing cron", payload: scanSchedulePayload{Name: "Anime", LibraryID: "anime"}},
		{name: "invalid cron", payload: scanSchedulePayload{Name: "Anime", LibraryID: "anime", Cron: "60 4 * * *"}},
		{name: "source without mount", payload: scanSchedulePayload{Name: "Anime", LibraryID: "anime", SourcePath: "/active", Cron: "0 4 * * *"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := toScanScheduleModel(tt.payload); err == nil {
				t.Fatal("toScanScheduleModel() error = nil")
			}
		})
	}
}

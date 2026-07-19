package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"NyaMedia/internal/model"
)

type scanSchedulePayload struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	LibraryID  string `json:"library_id"`
	MountID    string `json:"mount_id,omitempty"`
	SourcePath string `json:"source_path,omitempty"`
	Cron       string `json:"cron"`
	Enabled    bool   `json:"enabled"`
}

func (a *App) handleScanSchedules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := a.scanSchedules.List(r.Context())
		if err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var payload scanSchedulePayload
		if err := decodeJSON(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		item, err := toScanScheduleModel(payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if item.ID == "" {
			item.ID = newID("schedule")
		} else if existing, err := a.scanSchedules.Get(r.Context(), item.ID); err != nil {
			handleStorageError(w, err)
			return
		} else if existing != nil {
			writeError(w, http.StatusConflict, "scan schedule id already exists")
			return
		}
		if err := a.validateScanScheduleTarget(r.Context(), item); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.scanSchedules.Create(r.Context(), item); err != nil {
			handleStorageError(w, err)
			return
		}
		created, err := a.scanSchedules.Get(r.Context(), item.ID)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleScanScheduleRoutes(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/scan-schedules/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	a.handleScanScheduleByID(w, r, id)
}

func (a *App) handleScanScheduleByID(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		item, err := a.scanSchedules.Get(r.Context(), id)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		if item == nil {
			writeError(w, http.StatusNotFound, "resource not found")
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPut:
		var payload scanSchedulePayload
		if err := decodeJSON(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		payload.ID = id
		item, err := toScanScheduleModel(payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.validateScanScheduleTarget(r.Context(), item); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.scanSchedules.Update(r.Context(), item); err != nil {
			handleStorageError(w, err)
			return
		}
		updated, err := a.scanSchedules.Get(r.Context(), id)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		if err := a.scanSchedules.Delete(r.Context(), id); err != nil {
			handleStorageError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func toScanScheduleModel(payload scanSchedulePayload) (model.ScanSchedule, error) {
	item := model.ScanSchedule{
		ID:         strings.TrimSpace(payload.ID),
		Name:       strings.TrimSpace(payload.Name),
		LibraryID:  strings.TrimSpace(payload.LibraryID),
		MountID:    strings.TrimSpace(payload.MountID),
		SourcePath: strings.TrimSpace(payload.SourcePath),
		Cron:       strings.TrimSpace(payload.Cron),
		Enabled:    payload.Enabled,
	}
	if item.Name == "" {
		return model.ScanSchedule{}, fmt.Errorf("name is required")
	}
	if item.LibraryID == "" {
		return model.ScanSchedule{}, fmt.Errorf("library_id is required")
	}
	if item.Cron == "" {
		return model.ScanSchedule{}, fmt.Errorf("cron is required")
	}
	if _, err := parseCronSchedule(item.Cron); err != nil {
		return model.ScanSchedule{}, fmt.Errorf("invalid cron: %w", err)
	}
	if item.SourcePath != "" {
		if item.MountID == "" {
			return model.ScanSchedule{}, fmt.Errorf("source_path requires mount_id")
		}
		item.SourcePath = normalizeProviderPath(item.SourcePath)
	}
	return item, nil
}

func (a *App) validateScanScheduleTarget(ctx context.Context, item model.ScanSchedule) error {
	library, err := a.libraries.Get(ctx, item.LibraryID)
	if err != nil {
		return err
	}
	if library == nil {
		return fmt.Errorf("library %s not found", item.LibraryID)
	}
	if item.Enabled && !library.Enabled {
		return fmt.Errorf("library %s is disabled", item.LibraryID)
	}
	listMounts := a.libraries.ListMounts
	if item.Enabled {
		listMounts = a.libraries.ListEnabledMounts
	}
	mounts, err := listMounts(ctx, item.LibraryID)
	if err != nil {
		return err
	}
	_, err = resolveScanScheduleTargets(item, mounts)
	return err
}

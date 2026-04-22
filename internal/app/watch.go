package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"emby115/internal/model"
	provideriface "emby115/internal/provider"
	localprovider "emby115/internal/provider/local"
)

type providerWatchStatus struct {
	MountID       string `json:"mount_id"`
	ProviderID    string `json:"provider_id"`
	LibraryID     string `json:"library_id"`
	SourcePath    string `json:"source_path"`
	Capable       bool   `json:"capable"`
	Active        bool   `json:"active"`
	StartedAt     string `json:"started_at,omitempty"`
	LastEventAt   string `json:"last_event_at,omitempty"`
	LastEventType string `json:"last_event_type,omitempty"`
	LastEventPath string `json:"last_event_path,omitempty"`
	LastError     string `json:"last_error,omitempty"`
}

func (a *App) startProviderWatchers(ctx context.Context) {
	libraries, err := a.libraries.ListEnabled(context.Background())
	if err != nil {
		log.Printf("load libraries for watchers: %v", err)
		return
	}

	for _, library := range libraries {
		mounts, err := a.libraries.ListEnabledMounts(context.Background(), library.ID)
		if err != nil {
			log.Printf("load mounts for watcher library %s: %v", library.ID, err)
			continue
		}
		for _, mount := range mounts {
			a.startMountWatcher(ctx, library.ID, mount)
		}
	}
}

func (a *App) startMountWatcher(ctx context.Context, libraryID string, mount model.LibraryMount) {
	providerModel, err := a.providers.Get(context.Background(), mount.ProviderID)
	if err != nil {
		log.Printf("load provider %s for watcher: %v", mount.ProviderID, err)
		a.recordWatchStatus(providerWatchStatus{
			MountID:    mount.ID,
			ProviderID: mount.ProviderID,
			LibraryID:  libraryID,
			SourcePath: mount.SourcePath,
			LastError:  err.Error(),
		})
		a.recordSystemEvent(context.Background(), "provider_watch_error", "error", "watcher", "failed to load provider for watcher", map[string]any{
			"mount_id":    mount.ID,
			"provider_id": mount.ProviderID,
			"library_id":  libraryID,
			"source_path": mount.SourcePath,
			"error":       err.Error(),
		})
		return
	}
	if providerModel == nil || !providerModel.Enabled {
		a.recordWatchStatus(providerWatchStatus{
			MountID:    mount.ID,
			ProviderID: mount.ProviderID,
			LibraryID:  libraryID,
			SourcePath: mount.SourcePath,
			Capable:    a.providerWatchCapable(providerModel),
		})
		return
	}

	watchProvider, ok, err := a.buildWatchProvider(*providerModel)
	status := providerWatchStatus{
		MountID:    mount.ID,
		ProviderID: providerModel.ID,
		LibraryID:  libraryID,
		SourcePath: mount.SourcePath,
		Capable:    ok,
	}
	if err != nil {
		log.Printf("build watch provider %s: %v", providerModel.ID, err)
		status.LastError = err.Error()
		a.recordWatchStatus(status)
		a.recordSystemEvent(context.Background(), "provider_watch_error", "error", "watcher", "failed to build watch provider", map[string]any{
			"mount_id":    mount.ID,
			"provider_id": providerModel.ID,
			"library_id":  libraryID,
			"source_path": mount.SourcePath,
			"error":       err.Error(),
		})
		return
	}
	a.recordWatchStatus(status)
	if !ok {
		return
	}

	go func() {
		startedAt := time.Now().UTC().Format(time.RFC3339)
		a.recordWatchStatus(providerWatchStatus{
			MountID:    mount.ID,
			ProviderID: providerModel.ID,
			LibraryID:  libraryID,
			SourcePath: mount.SourcePath,
			Capable:    true,
			Active:     true,
			StartedAt:  startedAt,
		})
		log.Printf("starting watcher provider=%s library=%s source=%s", providerModel.ID, libraryID, mount.SourcePath)
		a.recordSystemEvent(context.Background(), "provider_watch_started", "info", "watcher", "started provider watcher", map[string]any{
			"mount_id":    mount.ID,
			"provider_id": providerModel.ID,
			"library_id":  libraryID,
			"source_path": mount.SourcePath,
		})
		err := watchProvider.Watch(ctx, mount.SourcePath, func(event provideriface.ChangeEvent) {
			if event.IsDir {
				return
			}
			log.Printf("provider change provider=%s library=%s type=%s path=%s", event.ProviderID, libraryID, event.Type, event.Path)
			now := time.Now().UTC().Format(time.RFC3339)
			a.recordWatchStatus(providerWatchStatus{
				MountID:       mount.ID,
				ProviderID:    providerModel.ID,
				LibraryID:     libraryID,
				SourcePath:    mount.SourcePath,
				Capable:       true,
				Active:        true,
				StartedAt:     startedAt,
				LastEventAt:   now,
				LastEventType: string(event.Type),
				LastEventPath: event.Path,
			})
			a.recordSystemEvent(context.Background(), "provider_watch_change", "info", "watcher", "provider change detected", map[string]any{
				"mount_id":    mount.ID,
				"provider_id": providerModel.ID,
				"library_id":  libraryID,
				"source_path": mount.SourcePath,
				"change_type": string(event.Type),
				"path":        event.Path,
				"detected_at": now,
			})
			a.scheduleLibraryRescan(libraryID, map[string]any{
				"provider_id": event.ProviderID,
				"change_type": event.Type,
				"path":        event.Path,
			})
		})
		if err != nil && ctx.Err() == nil {
			log.Printf("watcher stopped provider=%s library=%s source=%s error=%v", providerModel.ID, libraryID, mount.SourcePath, err)
			a.recordWatchStatus(providerWatchStatus{
				MountID:    mount.ID,
				ProviderID: providerModel.ID,
				LibraryID:  libraryID,
				SourcePath: mount.SourcePath,
				Capable:    true,
				LastError:  err.Error(),
			})
			a.recordSystemEvent(context.Background(), "provider_watch_error", "error", "watcher", "provider watcher stopped with error", map[string]any{
				"mount_id":    mount.ID,
				"provider_id": providerModel.ID,
				"library_id":  libraryID,
				"source_path": mount.SourcePath,
				"error":       err.Error(),
			})
			return
		}
		a.recordWatchStatus(providerWatchStatus{
			MountID:    mount.ID,
			ProviderID: providerModel.ID,
			LibraryID:  libraryID,
			SourcePath: mount.SourcePath,
			Capable:    true,
		})
		a.recordSystemEvent(context.Background(), "provider_watch_stopped", "info", "watcher", "provider watcher stopped", map[string]any{
			"mount_id":    mount.ID,
			"provider_id": providerModel.ID,
			"library_id":  libraryID,
			"source_path": mount.SourcePath,
		})
	}()
}

func (a *App) providerWatchCapable(providerModel *model.Provider) bool {
	if providerModel == nil {
		return false
	}
	switch providerModel.Type {
	case "local":
		return true
	default:
		return false
	}
}

func (a *App) buildWatchProvider(providerModel model.Provider) (provideriface.WatchProvider, bool, error) {
	switch providerModel.Type {
	case "local":
		p := localprovider.New(providerModel.ID, providerModel.RootPath)
		return p, true, nil
	default:
		return nil, false, nil
	}
}

func (a *App) scheduleLibraryRescan(libraryID string, reason any) {
	a.watchMu.Lock()
	defer a.watchMu.Unlock()

	if timer, ok := a.watchTimers[libraryID]; ok {
		timer.Stop()
	}

	a.watchTimers[libraryID] = time.AfterFunc(2*time.Second, func() {
		a.watchMu.Lock()
		delete(a.watchTimers, libraryID)
		a.watchMu.Unlock()

		if err := a.enqueueLibraryScan(context.Background(), libraryID, reason); err != nil {
			log.Printf("enqueue incremental scan library=%s: %v", libraryID, err)
		}
	})
}

func (a *App) enqueueLibraryScan(ctx context.Context, libraryID string, reason any) error {
	activeFull, err := a.tasks.FindActive(ctx, "full_scan", "")
	if err != nil {
		return err
	}
	if activeFull != nil {
		return nil
	}

	activeLibrary, err := a.tasks.FindActive(ctx, "library_scan", libraryID)
	if err != nil {
		return err
	}
	if activeLibrary != nil {
		return nil
	}

	library, err := a.libraries.Get(ctx, libraryID)
	if err != nil {
		return err
	}
	if library == nil || !library.Enabled {
		return nil
	}

	task, err := a.createScanTask(ctx, model.ScanTask{
		TaskType:  "library_scan",
		LibraryID: libraryID,
		Status:    model.TaskStatusPending,
		Message:   "queued incremental library scan",
	})
	if err != nil {
		return err
	}
	a.appendTaskLog(ctx, task.ID, "info", "queued from provider change", normalizeWatchPayload(reason))
	go a.runLibraryScanTask(task.ID, libraryID)
	return nil
}

func normalizeWatchPayload(reason any) any {
	switch value := reason.(type) {
	case map[string]any:
		payload := make(map[string]any, len(value))
		for key, item := range value {
			if changeType, ok := item.(provideriface.ChangeType); ok {
				payload[key] = string(changeType)
				continue
			}
			payload[key] = item
		}
		return payload
	case string:
		if strings.TrimSpace(value) == "" {
			return nil
		}
		return map[string]any{"reason": value}
	default:
		if value == nil {
			return nil
		}
		return map[string]any{"reason": fmt.Sprintf("%v", value)}
	}
}

func (a *App) recordWatchStatus(status providerWatchStatus) {
	a.watchMu.Lock()
	defer a.watchMu.Unlock()

	current := a.watchStatus[status.MountID]
	if status.MountID == "" {
		return
	}
	if status.ProviderID == "" {
		status.ProviderID = current.ProviderID
	}
	if status.LibraryID == "" {
		status.LibraryID = current.LibraryID
	}
	if status.SourcePath == "" {
		status.SourcePath = current.SourcePath
	}
	if status.StartedAt == "" {
		status.StartedAt = current.StartedAt
	}
	if status.LastEventAt == "" {
		status.LastEventAt = current.LastEventAt
	}
	if status.LastEventType == "" {
		status.LastEventType = current.LastEventType
	}
	if status.LastEventPath == "" {
		status.LastEventPath = current.LastEventPath
	}
	if status.LastError == "" {
		status.LastError = current.LastError
	}
	if current.Capable && !status.Capable {
		status.Capable = true
	}
	a.watchStatus[status.MountID] = status
}

func (a *App) listWatchStatusByProvider(providerID string) []providerWatchStatus {
	a.watchMu.Lock()
	defer a.watchMu.Unlock()

	items := make([]providerWatchStatus, 0)
	for _, item := range a.watchStatus {
		if item.ProviderID != providerID {
			continue
		}
		items = append(items, item)
	}
	return items
}

func (a *App) recordSystemEvent(ctx context.Context, eventType, level, source, message string, payload any) {
	payloadJSON := ""
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			log.Printf("marshal system event payload: %v", err)
		} else {
			payloadJSON = string(encoded)
		}
	}
	if err := a.events.Create(ctx, model.SystemEvent{
		ID:          newID("event"),
		EventType:   eventType,
		Level:       level,
		Source:      source,
		Message:     message,
		PayloadJSON: payloadJSON,
	}); err != nil {
		log.Printf("persist system event type=%s: %v", eventType, err)
	}
}

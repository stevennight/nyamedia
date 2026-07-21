package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"NyaMedia/internal/model"
	provideriface "NyaMedia/internal/provider"
	localprovider "NyaMedia/internal/provider/local"
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

type providerWatchTimer struct {
	timer *time.Timer
	done  sync.Once
}

func (a *App) startProviderWatchers(ctx context.Context) {
	var cancelGeneration context.CancelFunc
	var generation sync.WaitGroup
	restart := func() {
		if cancelGeneration != nil {
			cancelGeneration()
			generation.Wait()
			a.stopWatchTimers()
		}
		a.resetWatchStatus()
		generationCtx, cancel := context.WithCancel(ctx)
		cancelGeneration = cancel
		a.startProviderWatcherGeneration(generationCtx, &generation)
	}

	restart()
	for {
		select {
		case <-ctx.Done():
			if cancelGeneration != nil {
				cancelGeneration()
			}
			generation.Wait()
			a.stopWatchTimers()
			return
		case <-a.watchReload:
			restart()
		}
	}
}

func (a *App) startProviderWatcherGeneration(ctx context.Context, generation *sync.WaitGroup) {
	libraries, err := a.libraries.ListEnabled(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("load libraries for watchers: %v", err)
		}
		return
	}

	for _, library := range libraries {
		if err := ctx.Err(); err != nil {
			return
		}
		mounts, err := a.libraries.ListEnabledMounts(ctx, library.ID)
		if err != nil {
			log.Printf("load mounts for watcher library %s: %v", library.ID, err)
			continue
		}
		for _, mount := range mounts {
			if ctx.Err() != nil {
				return
			}
			a.startMountWatcher(ctx, generation, library.ID, mount)
		}
	}
}

func (a *App) startMountWatcher(ctx context.Context, generation *sync.WaitGroup, libraryID string, mount model.LibraryMount) {
	providerModel, err := a.providers.Get(ctx, mount.ProviderID)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Printf("load provider %s for watcher: %v", mount.ProviderID, err)
		a.recordWatchStatus(providerWatchStatus{
			MountID:    mount.ID,
			ProviderID: mount.ProviderID,
			LibraryID:  libraryID,
			SourcePath: mount.SourcePath,
			LastError:  err.Error(),
		})
		a.recordSystemEvent(ctx, "provider_watch_error", "error", "watcher", "failed to load provider for watcher", map[string]any{
			"mount_id":    mount.ID,
			"provider_id": mount.ProviderID,
			"library_id":  libraryID,
			"source_path": mount.SourcePath,
			"error":       err.Error(),
		})
		return
	}
	if providerModel == nil || !providerModel.Enabled || !providerModel.WatchEnabled {
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
		a.recordSystemEvent(ctx, "provider_watch_error", "error", "watcher", "failed to build watch provider", map[string]any{
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

	generation.Add(1)
	go func() {
		defer generation.Done()
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
		a.recordSystemEvent(ctx, "provider_watch_started", "info", "watcher", "started provider watcher", map[string]any{
			"mount_id":    mount.ID,
			"provider_id": providerModel.ID,
			"library_id":  libraryID,
			"source_path": mount.SourcePath,
		})
		err := watchProvider.Watch(ctx, mount.SourcePath, func(event provideriface.ChangeEvent) {
			if ctx.Err() != nil {
				return
			}
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
			a.recordSystemEvent(ctx, "provider_watch_change", "info", "watcher", "provider change detected", map[string]any{
				"mount_id":    mount.ID,
				"provider_id": providerModel.ID,
				"library_id":  libraryID,
				"source_path": mount.SourcePath,
				"change_type": string(event.Type),
				"path":        event.Path,
				"detected_at": now,
			})
			a.scheduleLibraryRescan(ctx, libraryID, map[string]any{
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
			a.recordSystemEvent(ctx, "provider_watch_error", "error", "watcher", "provider watcher stopped with error", map[string]any{
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
		a.recordSystemEvent(ctx, "provider_watch_stopped", "info", "watcher", "provider watcher stopped", map[string]any{
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

func (a *App) requestProviderWatcherReload() {
	select {
	case a.watchReload <- struct{}{}:
	default:
	}
}

func (a *App) scheduleLibraryRescan(ctx context.Context, libraryID string, reason any) {
	if ctx.Err() != nil {
		return
	}

	a.watchMu.Lock()
	defer a.watchMu.Unlock()

	if scheduled, ok := a.watchTimers[libraryID]; ok {
		if scheduled.timer.Stop() {
			scheduled.done.Do(a.watchTimerWG.Done)
		}
	}

	scheduled := &providerWatchTimer{}
	a.watchTimerWG.Add(1)
	scheduled.timer = time.AfterFunc(2*time.Second, func() {
		defer scheduled.done.Do(a.watchTimerWG.Done)
		a.watchMu.Lock()
		if a.watchTimers[libraryID] != scheduled {
			a.watchMu.Unlock()
			return
		}
		delete(a.watchTimers, libraryID)
		a.watchMu.Unlock()

		if ctx.Err() != nil {
			return
		}
		if err := a.enqueueLibraryScan(ctx, libraryID, reason); err != nil && ctx.Err() == nil {
			log.Printf("enqueue incremental scan library=%s: %v", libraryID, err)
		}
	})
	a.watchTimers[libraryID] = scheduled
}

func (a *App) stopWatchTimers() {
	a.watchMu.Lock()
	for _, scheduled := range a.watchTimers {
		if scheduled.timer.Stop() {
			scheduled.done.Do(a.watchTimerWG.Done)
		}
	}
	a.watchTimers = make(map[string]*providerWatchTimer)
	a.watchMu.Unlock()
	a.watchTimerWG.Wait()
}

func (a *App) resetWatchStatus() {
	a.watchMu.Lock()
	defer a.watchMu.Unlock()
	a.watchStatus = make(map[string]providerWatchStatus)
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
	}); err != nil && ctx.Err() == nil {
		log.Printf("persist system event type=%s: %v", eventType, err)
	}
}

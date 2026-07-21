package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"NyaMedia/internal/model"
)

const (
	scanQueueModeCurrentLevel = "current_level"
	scanQueueModeRecursive    = "recursive"
	scanQueueWebhookDebounce  = 2 * time.Minute
)

type scanQueueOptions struct {
	Overwrite bool `json:"overwrite"`
}

type queuedScanExecution struct {
	item    model.ScanQueueItem
	task    model.ScanTask
	options scanOptions
}

func (a *App) handleScanQueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	items, err := a.scanQueue.List(r.Context())
	if err != nil {
		handleStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) startScanQueueWorker(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	slots := make(chan struct{}, maxConcurrentLibraryScans)
	wake := make(chan struct{}, 1)
	var running sync.WaitGroup
	drain := func() {
		a.drainScanQueue(ctx, slots, &running, wake)
	}
	drain()

	for {
		select {
		case <-ctx.Done():
			running.Wait()
			return
		case <-ticker.C:
			drain()
		case <-wake:
			drain()
		}
	}
}

func (a *App) drainScanQueue(ctx context.Context, slots chan struct{}, running *sync.WaitGroup, wake chan<- struct{}) {
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
		default:
			return
		}
		releaseSlot := func() { <-slots }

		item, err := a.scanQueue.FirstDueExcludingProviders(ctx, time.Now().UTC().Format(time.RFC3339), a.activeProviderIDs())
		if err != nil {
			releaseSlot()
			log.Printf("load due scan queue item: %v", err)
			return
		}
		if item == nil {
			releaseSlot()
			return
		}

		execution, continueDraining := a.prepareQueuedScan(ctx, *item)
		if execution == nil {
			releaseSlot()
			if continueDraining {
				continue
			}
			return
		}

		running.Add(1)
		go func(run queuedScanExecution) {
			defer func() {
				a.unlockProvider(run.item.ProviderID)
				releaseSlot()
				running.Done()
				select {
				case wake <- struct{}{}:
				default:
				}
			}()
			a.runQueuedScan(ctx, run)
		}(*execution)
	}
}

func (a *App) prepareQueuedScan(ctx context.Context, item model.ScanQueueItem) (*queuedScanExecution, bool) {
	if !a.tryLockProvider(item.ProviderID) {
		return nil, true
	}
	keepProviderLock := false
	defer func() {
		if !keepProviderLock {
			a.unlockProvider(item.ProviderID)
		}
	}()

	if ok, err := a.validateQueuedScanTarget(ctx, item); err != nil || !ok {
		if err != nil {
			a.recordSystemEvent(ctx, "scan_queue_invalid", "warning", "scan_queue", "queued scan target is no longer valid", map[string]any{"queue_id": item.ID, "error": err.Error()})
		}
		if deleteErr := a.scanQueue.Delete(ctx, item.ID); deleteErr != nil {
			log.Printf("delete invalid scan queue item %s: %v", item.ID, deleteErr)
			return nil, false
		}
		return nil, true
	}

	options := scanOptionsFromQueue(item.OptionsJSON)
	taskType := "library_scan"
	message := "queued scan task from scan queue"
	if item.Mode == scanQueueModeCurrentLevel {
		message = "queued current-level scan task from scan queue"
	}
	task, err := a.scanQueue.DeleteAndCreateTask(ctx, item.ID, model.ScanTask{
		ID:        newID("task"),
		TaskType:  taskType,
		LibraryID: item.LibraryID,
		Status:    model.TaskStatusPending,
		Message:   message,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		log.Printf("dequeue scan queue item %s: %v", item.ID, err)
		return nil, false
	}
	a.appendTaskLog(ctx, task.ID, "info", "dequeued scan queue item", map[string]any{"queue_id": item.ID, "mount_id": item.MountID, "provider_id": item.ProviderID, "source_path": item.SourcePath, "mode": item.Mode, "source": item.Source, "events": item.EventCount, "reason": rawJSONMap(item.ReasonJSON)})

	keepProviderLock = true
	return &queuedScanExecution{item: item, task: *task, options: options}, true
}

func (a *App) runQueuedScan(ctx context.Context, run queuedScanExecution) {
	if run.item.Mode == scanQueueModeCurrentLevel {
		a.runLibraryCurrentLevelScanTask(ctx, run.task.ID, run.item.LibraryID, run.item.MountID, run.item.ProviderID, run.item.SourcePath, run.options)
		return
	}
	a.runLibraryScanTask(ctx, run.task.ID, run.item.LibraryID, run.item.MountID, run.item.ProviderID, run.item.SourcePath, "", run.options)
}

func (a *App) tryLockProvider(providerID string) bool {
	a.activeProviderMu.Lock()
	defer a.activeProviderMu.Unlock()
	if _, ok := a.activeProviders[providerID]; ok {
		return false
	}
	a.activeProviders[providerID] = struct{}{}
	return true
}

func (a *App) unlockProvider(providerID string) {
	a.activeProviderMu.Lock()
	defer a.activeProviderMu.Unlock()
	delete(a.activeProviders, providerID)
}

func (a *App) activeProviderIDs() []string {
	a.activeProviderMu.Lock()
	defer a.activeProviderMu.Unlock()
	providerIDs := make([]string, 0, len(a.activeProviders))
	for providerID := range a.activeProviders {
		providerIDs = append(providerIDs, providerID)
	}
	return providerIDs
}

func (a *App) validateQueuedScanTarget(ctx context.Context, item model.ScanQueueItem) (bool, error) {
	library, err := a.libraries.Get(ctx, item.LibraryID)
	if err != nil || library == nil || !library.Enabled {
		return false, err
	}
	mounts, err := a.libraries.ListEnabledMounts(ctx, item.LibraryID)
	if err != nil {
		return false, err
	}
	if item.MountID != "" {
		mount, ok := findLibraryMountByID(mounts, item.MountID)
		if !ok {
			return false, fmt.Errorf("mount %s not found or disabled", item.MountID)
		}
		if mount.ProviderID != item.ProviderID || !providerPathWithinRoot(item.SourcePath, mount.SourcePath) {
			return false, fmt.Errorf("queued source path %s no longer matches mount %s", item.SourcePath, item.MountID)
		}
		return true, nil
	}
	mount, ok := findMountForSourcePath(mounts, item.SourcePath)
	if !ok || mount.ProviderID != item.ProviderID {
		return false, fmt.Errorf("source path %s is not under provider %s in library %s", item.SourcePath, item.ProviderID, item.LibraryID)
	}
	return true, nil
}

func (a *App) enqueueScan(ctx context.Context, libraryID, mountID, providerID, sourcePath, mode, source string, runAfter time.Time, reason any, options scanOptions) (*model.ScanQueueItem, error) {
	sourcePath = normalizeProviderPath(sourcePath)
	optionsJSON := mustJSON(scanQueueOptions{Overwrite: options.Overwrite})
	reasonJSON := mustJSON(normalizeWatchPayload(reason))
	now := time.Now().UTC().Format(time.RFC3339)
	if covering, err := a.scanQueue.FindCoveringRecursive(ctx, libraryID, mountID, providerID, sourcePath); err != nil {
		return nil, err
	} else if covering != nil {
		return a.scanQueue.Touch(ctx, covering.ID, source, now, reasonJSON)
	}
	item, err := a.scanQueue.Upsert(ctx, model.ScanQueueItem{
		ID:          newID("queue"),
		LibraryID:   libraryID,
		MountID:     mountID,
		ProviderID:  providerID,
		SourcePath:  sourcePath,
		Mode:        mode,
		Source:      source,
		RunAfter:    runAfter.UTC().Format(time.RFC3339),
		LastEventAt: now,
		OptionsJSON: optionsJSON,
		ReasonJSON:  reasonJSON,
	})
	if err != nil {
		return nil, err
	}
	if mode == scanQueueModeRecursive {
		if err := a.scanQueue.DeleteCovered(ctx, libraryID, mountID, providerID, sourcePath); err != nil {
			return nil, err
		}
	}
	return item, nil
}

func (a *App) enqueueLibraryScan(ctx context.Context, libraryID string, reason any) error {
	mounts, err := a.libraries.ListEnabledMounts(ctx, libraryID)
	if err != nil {
		return err
	}
	for _, mount := range mounts {
		if _, err := a.enqueueScan(ctx, libraryID, mount.ID, mount.ProviderID, mount.SourcePath, scanQueueModeRecursive, "watch", time.Now(), reason, scanOptions{}); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) enqueueScanSchedule(ctx context.Context, item model.ScanSchedule, reason any) error {
	library, err := a.libraries.Get(ctx, item.LibraryID)
	if err != nil {
		return err
	}
	if library == nil {
		return fmt.Errorf("library %s not found", item.LibraryID)
	}
	if !library.Enabled {
		return fmt.Errorf("library %s is disabled", item.LibraryID)
	}

	mounts, err := a.libraries.ListEnabledMounts(ctx, item.LibraryID)
	if err != nil {
		return err
	}
	targets, err := resolveScanScheduleTargets(item, mounts)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if _, err := a.enqueueScan(ctx, item.LibraryID, target.Mount.ID, target.Mount.ProviderID, target.SourcePath, scanQueueModeRecursive, "schedule", time.Now(), reason, scanOptions{}); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) enqueueManualLibraryScan(ctx context.Context, libraryID string, payload scanLibraryPayload) ([]*model.ScanQueueItem, error) {
	mounts, err := a.libraries.ListEnabledMounts(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	options := scanOptions{Overwrite: payload.Overwrite}
	items := make([]*model.ScanQueueItem, 0)
	mountID := strings.TrimSpace(payload.MountID)
	var selectedMount model.LibraryMount
	hasSelectedMount := false
	if mountID != "" {
		var ok bool
		selectedMount, ok = findLibraryMountByID(mounts, mountID)
		if !ok {
			return nil, fmt.Errorf("mount %s is not enabled for library %s", mountID, libraryID)
		}
		hasSelectedMount = true
	}
	if strings.TrimSpace(payload.TargetPath) != "" {
		targetPath := normalizeProviderPath(payload.TargetPath)
		if hasSelectedMount {
			if !providerPathWithinRoot(targetPath, selectedMount.TargetPath) {
				return nil, fmt.Errorf("target path %s is not under mount %s target path %s", targetPath, selectedMount.ID, selectedMount.TargetPath)
			}
			payload.SourcePath = sourcePathForMountTargetPath(selectedMount, targetPath)
		} else {
			var ok bool
			selectedMount, ok = findMountForTargetPath(mounts, targetPath)
			if !ok {
				return nil, fmt.Errorf("target path %s is not under an enabled mount for library %s", targetPath, libraryID)
			}
			hasSelectedMount = true
			payload.SourcePath = sourcePathForMountTargetPath(selectedMount, targetPath)
		}
	}
	if strings.TrimSpace(payload.SourcePath) != "" {
		sourcePath := normalizeProviderPath(payload.SourcePath)
		mount := selectedMount
		if hasSelectedMount {
			if !providerPathWithinRoot(sourcePath, mount.SourcePath) {
				return nil, fmt.Errorf("source path %s is not under mount %s source path %s", sourcePath, mount.ID, mount.SourcePath)
			}
		} else {
			var ok bool
			mount, ok = findMountForSourcePath(mounts, sourcePath)
			if !ok {
				return nil, fmt.Errorf("source path %s is not under an enabled mount for library %s", sourcePath, libraryID)
			}
		}
		item, err := a.enqueueScan(ctx, libraryID, mount.ID, mount.ProviderID, sourcePath, scanQueueModeRecursive, "manual", time.Now(), map[string]any{"source": "manual_library_scan", "mount_id": mount.ID, "source_path": sourcePath}, options)
		if err != nil {
			return nil, err
		}
		return append(items, item), nil
	}
	if hasSelectedMount {
		item, err := a.enqueueScan(ctx, libraryID, selectedMount.ID, selectedMount.ProviderID, selectedMount.SourcePath, scanQueueModeRecursive, "manual", time.Now(), map[string]any{"source": "manual_library_scan", "mount_id": selectedMount.ID}, options)
		if err != nil {
			return nil, err
		}
		return append(items, item), nil
	}
	for _, mount := range mounts {
		item, err := a.enqueueScan(ctx, libraryID, mount.ID, mount.ProviderID, mount.SourcePath, scanQueueModeRecursive, "manual", time.Now(), map[string]any{"source": "manual_library_scan"}, options)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (a *App) enqueueLibraryCurrentLevelScan(ctx context.Context, libraryID, mountID, providerID, sourcePath string, reason any, options scanOptions) (bool, error) {
	mounts, err := a.libraries.ListEnabledMounts(ctx, libraryID)
	if err != nil {
		return false, err
	}
	mount, ok := findLibraryMountByID(mounts, mountID)
	if !ok || mount.ProviderID != providerID || !providerPathWithinRoot(normalizeProviderPath(sourcePath), mount.SourcePath) {
		return false, nil
	}
	_, err = a.enqueueScan(ctx, libraryID, mount.ID, mount.ProviderID, sourcePath, scanQueueModeCurrentLevel, "webhook", time.Now().Add(scanQueueWebhookDebounce), reason, options)
	if err != nil {
		return false, err
	}
	return true, nil
}

func scanOptionsFromQueue(value string) scanOptions {
	var options scanQueueOptions
	_ = json.Unmarshal([]byte(value), &options)
	return scanOptions{Overwrite: options.Overwrite}
}

func mustJSON(value any) string {
	if value == nil {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func rawJSONMap(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return value
	}
	return decoded
}

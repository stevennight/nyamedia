package app

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"time"
)

const (
	scanLogRetentionSettingKey     = "scan.log_retention_days"
	systemEventRetentionSettingKey = "system.event_retention_days"
	systemTimezoneSettingKey       = "system.timezone"
)

func (a *App) startScanLogPruner(ctx context.Context) {
	a.pruneExpiredScanLogs(ctx)
	a.pruneExpiredSystemEvents(ctx)

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.pruneExpiredScanLogs(ctx)
			a.pruneExpiredSystemEvents(ctx)
		}
	}
}

func (a *App) pruneExpiredScanLogs(ctx context.Context) {
	retentionDays, err := a.scanLogRetentionDays(ctx)
	if err != nil {
		log.Printf("load scan log retention: %v", err)
		return
	}
	if retentionDays <= 0 {
		return
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays).Format(time.RFC3339)
	deleted, err := a.tasks.DeleteFinishedBefore(ctx, cutoff)
	if err != nil {
		log.Printf("delete expired scan logs cutoff=%s: %v", cutoff, err)
		a.recordSystemEvent(ctx, "scan_log_prune_error", "error", "pruner", "failed to delete expired scan logs", map[string]any{"cutoff": cutoff, "retention_days": retentionDays, "error": err.Error()})
		return
	}
	if deleted > 0 {
		log.Printf("deleted expired scan logs cutoff=%s tasks=%d retention_days=%d", cutoff, deleted, retentionDays)
		a.recordSystemEvent(ctx, "scan_log_pruned", "info", "pruner", "deleted expired scan logs", map[string]any{"cutoff": cutoff, "tasks": deleted, "retention_days": retentionDays})
	}
}

func (a *App) scanLogRetentionDays(ctx context.Context) (int, error) {
	return a.retentionDays(ctx, scanLogRetentionSettingKey)
}

func (a *App) pruneExpiredSystemEvents(ctx context.Context) {
	retentionDays, err := a.systemEventRetentionDays(ctx)
	if err != nil {
		log.Printf("load system event retention: %v", err)
		return
	}
	if retentionDays <= 0 {
		return
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays).Format("2006-01-02 15:04:05")
	deleted, err := a.events.DeleteBefore(ctx, cutoff)
	if err != nil {
		log.Printf("delete expired system events cutoff=%s: %v", cutoff, err)
		a.recordSystemEvent(ctx, "system_event_prune_error", "error", "pruner", "failed to delete expired system events", map[string]any{"cutoff": cutoff, "retention_days": retentionDays, "error": err.Error()})
		return
	}
	if deleted > 0 {
		log.Printf("deleted expired system events cutoff=%s events=%d retention_days=%d", cutoff, deleted, retentionDays)
		a.recordSystemEvent(ctx, "system_events_pruned", "info", "pruner", "deleted expired system events", map[string]any{"cutoff": cutoff, "events": deleted, "retention_days": retentionDays})
	}
}

func (a *App) systemEventRetentionDays(ctx context.Context) (int, error) {
	return a.retentionDays(ctx, systemEventRetentionSettingKey)
}

func (a *App) retentionDays(ctx context.Context, key string) (int, error) {
	item, err := a.settings.Get(ctx, key)
	if err != nil || item == nil {
		return 0, err
	}

	var number int
	if err := json.Unmarshal([]byte(item.ValueJSON), &number); err == nil {
		return number, nil
	}

	var text string
	if err := json.Unmarshal([]byte(item.ValueJSON), &text); err != nil {
		return 0, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	return strconv.Atoi(text)
}

package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"NyaMedia/internal/model"
)

type filesystemWebhookPayload struct {
	Event           string `json:"event"`
	Path            string `json:"path"`
	SourcePath      string `json:"source_path"`
	DestinationPath string `json:"destination_path"`
	ProviderID      string `json:"provider_id"`
	LibraryID       string `json:"library_id"`
	IsDir           *bool  `json:"is_dir"`
	Overwrite       bool   `json:"overwrite"`
}

type webhookScanTarget struct {
	LibraryID  string
	MountID    string
	ProviderID string
	SourcePath string
}

func (a *App) handleFilesystemWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.recordSystemEvent(r.Context(), "webhook_method_not_allowed", "warning", "webhook", "webhook request used unsupported method", webhookRequestPayload(r, nil))
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !a.authorizeWebhook(r) {
		a.recordSystemEvent(r.Context(), "webhook_auth_failed", "warning", "webhook", "webhook request failed authentication", webhookRequestPayload(r, nil))
		writeError(w, http.StatusUnauthorized, "invalid webhook token")
		return
	}

	payload, raw, err := decodeFilesystemWebhook(r)
	if err != nil {
		a.recordSystemEvent(r.Context(), "webhook_payload_error", "warning", "webhook", "webhook payload could not be parsed", webhookRequestPayload(r, map[string]any{"error": err.Error()}))
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.recordSystemEvent(r.Context(), "webhook_received", "info", "webhook", "webhook request received", webhookPayload(r, payload, raw, nil))

	targets, err := a.findWebhookScanTargets(r.Context(), payload)
	if err != nil {
		handleStorageError(w, err)
		return
	}
	if len(targets) == 0 {
		a.recordSystemEvent(r.Context(), "webhook_no_match", "warning", "webhook", "webhook path did not match any enabled mount", map[string]any{
			"endpoint":         r.URL.Path,
			"remote_addr":      r.RemoteAddr,
			"user_agent":       r.UserAgent(),
			"path":             firstNonEmpty(payload.SourcePath, payload.Path),
			"destination_path": payload.DestinationPath,
			"provider_id":      payload.ProviderID,
			"library_id":       payload.LibraryID,
			"event":            payload.Event,
			"payload":          raw,
		})
		writeJSON(w, http.StatusAccepted, map[string]any{"matched": 0, "queued": 0})
		return
	}
	if isWebhookDeleteEvent(payload.Event) {
		deleted, err := a.cleanupWebhookDeletedTargets(r.Context(), payload)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		queued := 0
		for _, target := range targets {
			reason := map[string]any{
				"source":           "webhook_delete_reconcile",
				"event":            payload.Event,
				"path":             firstNonEmpty(payload.SourcePath, payload.Path),
				"destination_path": payload.DestinationPath,
				"scan_path":        target.SourcePath,
				"mount_id":         target.MountID,
				"provider_id":      target.ProviderID,
				"library_id":       target.LibraryID,
			}
			if raw != nil {
				reason["payload"] = raw
			}
			created, err := a.enqueueLibraryCurrentLevelScan(r.Context(), target.LibraryID, target.MountID, target.ProviderID, target.SourcePath, reason, scanOptions{Overwrite: payload.Overwrite})
			if err != nil {
				handleStorageError(w, err)
				return
			}
			if created {
				queued++
			}
		}
		a.recordSystemEvent(r.Context(), "webhook_cleaned", "info", "webhook", "webhook cleaned deleted output", webhookPayload(r, payload, raw, map[string]any{"matched": len(targets), "queued": queued, "deleted": deleted}))
		writeJSON(w, http.StatusAccepted, map[string]any{"matched": len(targets), "queued": queued, "deleted": deleted})
		return
	}

	queued := 0
	for _, target := range targets {
		reason := map[string]any{
			"source":           "webhook",
			"event":            payload.Event,
			"path":             firstNonEmpty(payload.SourcePath, payload.Path),
			"destination_path": payload.DestinationPath,
			"scan_path":        target.SourcePath,
			"mount_id":         target.MountID,
			"provider_id":      target.ProviderID,
			"library_id":       target.LibraryID,
		}
		if raw != nil {
			reason["payload"] = raw
		}
		created, err := a.enqueueLibraryCurrentLevelScan(r.Context(), target.LibraryID, target.MountID, target.ProviderID, target.SourcePath, reason, scanOptions{Overwrite: payload.Overwrite})
		if err != nil {
			handleStorageError(w, err)
			return
		}
		if created {
			queued++
		}
	}
	a.recordSystemEvent(r.Context(), "webhook_queued", "info", "webhook", "webhook queued current-level scan", webhookPayload(r, payload, raw, map[string]any{"matched": len(targets), "queued": queued}))

	writeJSON(w, http.StatusAccepted, map[string]any{"matched": len(targets), "queued": queued})
}

func webhookRequestPayload(r *http.Request, extra map[string]any) map[string]any {
	payload := map[string]any{
		"endpoint":    r.URL.Path,
		"method":      r.Method,
		"remote_addr": r.RemoteAddr,
		"user_agent":  r.UserAgent(),
	}
	for key, value := range extra {
		payload[key] = value
	}
	return payload
}

func webhookPayload(r *http.Request, payload filesystemWebhookPayload, raw map[string]any, extra map[string]any) map[string]any {
	value := webhookRequestPayload(r, map[string]any{
		"event":            payload.Event,
		"path":             firstNonEmpty(payload.SourcePath, payload.Path),
		"destination_path": payload.DestinationPath,
		"provider_id":      payload.ProviderID,
		"library_id":       payload.LibraryID,
		"payload":          raw,
	})
	for key, extraValue := range extra {
		value[key] = extraValue
	}
	return value
}

func (a *App) authorizeWebhook(r *http.Request) bool {
	expected := strings.TrimSpace(a.config.Webhook.Token)
	if expected == "" {
		return false
	}
	actual := strings.TrimSpace(r.URL.Query().Get("token"))
	if actual == "" {
		actual = strings.TrimSpace(r.Header.Get("X-Webhook-Token"))
	}
	if actual == "" {
		actual = strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func decodeFilesystemWebhook(r *http.Request) (filesystemWebhookPayload, map[string]any, error) {
	defer r.Body.Close()
	var raw map[string]any
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := decoder.Decode(&raw); err != nil {
		return filesystemWebhookPayload{}, nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return filesystemWebhookPayload{}, nil, fmt.Errorf("request body must contain a single JSON object")
		}
		return filesystemWebhookPayload{}, nil, err
	}

	payload := filesystemWebhookPayload{
		Event:           stringFromMap(raw, "event", "type", "action"),
		Path:            stringFromMap(raw, "path", "file_path", "filepath", "full_path"),
		SourcePath:      stringFromMap(raw, "source_path", "sourcePath", "source_file", "provider_path", "providerPath"),
		DestinationPath: stringFromMap(raw, "destination_path", "destinationPath", "destination_file"),
		ProviderID:      stringFromMap(raw, "provider_id", "providerId"),
		LibraryID:       stringFromMap(raw, "library_id", "libraryId"),
		Overwrite:       true,
	}
	if value, ok := boolFromMap(raw, "is_dir", "isDir", "directory"); ok {
		payload.IsDir = &value
	}
	if value, ok := boolFromMap(raw, "overwrite", "force", "replace"); ok {
		payload.Overwrite = value
	} else if value, ok := boolFromMap(map[string]any{"overwrite": r.URL.Query().Get("overwrite")}, "overwrite"); ok {
		payload.Overwrite = value
	}
	if strings.TrimSpace(payload.Event) == "" {
		payload.Event = "change"
	}
	if strings.TrimSpace(firstNonEmpty(payload.SourcePath, payload.Path, payload.DestinationPath)) == "" {
		return filesystemWebhookPayload{}, raw, fmt.Errorf("path or source_path is required")
	}
	return payload, raw, nil
}

func (a *App) findWebhookScanTargets(ctx context.Context, payload filesystemWebhookPayload) ([]webhookScanTarget, error) {
	if strings.TrimSpace(payload.ProviderID) == "" {
		return nil, nil
	}

	libraries, err := a.libraries.ListEnabled(ctx)
	if err != nil {
		return nil, err
	}

	targets := make([]webhookScanTarget, 0)
	seen := make(map[string]struct{})
	for _, library := range libraries {
		if payload.LibraryID != "" && library.ID != payload.LibraryID {
			continue
		}
		mounts, err := a.libraries.ListEnabledMounts(ctx, library.ID)
		if err != nil {
			return nil, err
		}
		for _, mount := range mounts {
			if mount.ProviderID != payload.ProviderID {
				continue
			}
			webhookPaths, err := a.webhookPayloadPathsForProvider(ctx, mount.ProviderID, payload)
			if err != nil {
				return nil, err
			}
			for _, webhookPath := range webhookPaths {
				scanPath := webhookScanPath(webhookPath, payload.IsDir)
				if !providerPathWithinRoot(webhookPath, mount.SourcePath) {
					continue
				}
				if !providerPathWithinRoot(scanPath, mount.SourcePath) {
					continue
				}
				targetScanPath := scanPath
				key := library.ID + "\x00" + mount.ID + "\x00" + mount.ProviderID + "\x00" + targetScanPath
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				targets = append(targets, webhookScanTarget{LibraryID: library.ID, MountID: mount.ID, ProviderID: mount.ProviderID, SourcePath: targetScanPath})
			}
		}
	}
	return targets, nil
}

func webhookPayloadPaths(payload filesystemWebhookPayload) []string {
	return webhookPayloadPathsRaw(payload)
}

func webhookPayloadPathsRaw(payload filesystemWebhookPayload) []string {
	values := []string{firstNonEmpty(payload.SourcePath, payload.Path), payload.DestinationPath}
	paths := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		normalized := normalizeProviderPath(value)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}
	return paths
}

func (a *App) webhookPayloadPathsForProvider(ctx context.Context, providerID string, payload filesystemWebhookPayload) ([]string, error) {
	if payload.ProviderID == "" || payload.ProviderID != providerID {
		return nil, nil
	}
	provider, err := a.providers.Get(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if provider == nil || !provider.Enabled {
		return nil, nil
	}
	prefixes := providerWebhookPathPrefixes(*provider)
	if len(prefixes) == 0 {
		return nil, nil
	}
	return webhookPayloadPathsWithPrefixes(payload, prefixes), nil
}

func webhookPayloadPathsWithPrefixes(payload filesystemWebhookPayload, stripPrefixes []string) []string {
	values := []string{firstNonEmpty(payload.SourcePath, payload.Path), payload.DestinationPath}
	paths := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		normalized, ok := stripProviderPathPrefixes(normalizeProviderPath(value), stripPrefixes)
		if !ok {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}
	return paths
}

func providerWebhookPathPrefixes(provider model.Provider) []string {
	if strings.TrimSpace(provider.ConfigJSON) == "" {
		return nil
	}
	var cfg providerConfig
	if err := json.Unmarshal([]byte(provider.ConfigJSON), &cfg); err != nil || cfg.Webhook == nil {
		return nil
	}
	items := make([]string, 0, len(cfg.Webhook.PathPrefixes))
	seen := make(map[string]struct{})
	for _, prefix := range cfg.Webhook.PathPrefixes {
		clean := normalizeProviderPath(prefix)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		items = append(items, clean)
	}
	return items
}

func stripProviderPathPrefixes(providerPath string, stripPrefixes []string) (string, bool) {
	providerPath = normalizeProviderPath(providerPath)
	for _, prefix := range stripPrefixes {
		cleanPrefix := normalizeProviderPath(prefix)
		if !providerPathWithinRoot(providerPath, cleanPrefix) {
			continue
		}
		stripped := strings.TrimPrefix(providerPath, cleanPrefix)
		stripped = strings.TrimPrefix(stripped, "/")
		return normalizeProviderPath(stripped), true
	}
	return "", false
}

func webhookScanPath(providerPath string, isDir *bool) string {
	providerPath = normalizeProviderPath(providerPath)
	if isDir != nil && *isDir {
		return providerPath
	}
	parent := path.Dir(providerPath)
	if parent == "." {
		return "/"
	}
	return normalizeProviderPath(parent)
}

func isWebhookDeleteEvent(event string) bool {
	switch strings.ToLower(strings.TrimSpace(event)) {
	case "delete", "deleted", "remove", "removed", "unlink", "unlinked":
		return true
	default:
		return false
	}
}

func stringFromMap(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return strings.TrimSpace(typed)
			}
		case fmt.Stringer:
			text := strings.TrimSpace(typed.String())
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func boolFromMap(values map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed, true
		case string:
			switch strings.ToLower(strings.TrimSpace(typed)) {
			case "true", "1", "yes":
				return true, true
			case "false", "0", "no":
				return false, true
			}
		}
	}
	return false, false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

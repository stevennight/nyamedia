package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"emby115/internal/model"
)

var embyServerKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{1,62}$`)

type embyServerPayload struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	UpstreamURL string `json:"upstream_url"`
	APIKey      string `json:"api_key,omitempty"`
	Enabled     bool   `json:"enabled"`
}

func (a *App) handleEmbyServers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := a.embyServers.List(r.Context())
		if err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var payload embyServerPayload
		if err := decodeJSON(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		item, err := toEmbyServerModel(payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if existing, err := a.embyServers.Get(r.Context(), item.Key); err != nil {
			handleStorageError(w, err)
			return
		} else if existing != nil {
			writeError(w, http.StatusConflict, "emby server key already exists")
			return
		}
		if existing, err := a.embyServers.GetByName(r.Context(), item.Name); err != nil {
			handleStorageError(w, err)
			return
		} else if existing != nil {
			writeError(w, http.StatusConflict, "emby server name already exists")
			return
		}
		if err := a.embyServers.Create(r.Context(), item); err != nil {
			handleStorageError(w, err)
			return
		}
		created, err := a.embyServers.Get(r.Context(), item.Key)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleEmbyServerRoutes(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/api/v1/emby-servers/")
	if key == "" || strings.Contains(key, "/") {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	a.handleEmbyServerByKey(w, r, key)
}

func (a *App) handleEmbyServerByKey(w http.ResponseWriter, r *http.Request, key string) {
	switch r.Method {
	case http.MethodGet:
		item, err := a.embyServers.Get(r.Context(), key)
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
		var payload embyServerPayload
		if err := decodeJSON(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		payload.Key = key
		item, err := toEmbyServerModel(payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if existing, err := a.embyServers.GetByName(r.Context(), item.Name); err != nil {
			handleStorageError(w, err)
			return
		} else if existing != nil && existing.Key != key {
			writeError(w, http.StatusConflict, "emby server name already exists")
			return
		}
		if err := a.embyServers.Update(r.Context(), item); err != nil {
			handleStorageError(w, err)
			return
		}
		updated, err := a.embyServers.Get(r.Context(), key)
		if err != nil {
			handleStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		if err := a.embyServers.Delete(r.Context(), key); err != nil {
			handleStorageError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleEmbyProxy(w http.ResponseWriter, r *http.Request) {
	if normalizedPath, ok := normalizeEmbyProxyBasePath(r.URL.Path); ok {
		redirectURL := *r.URL
		redirectURL.Path = normalizedPath
		http.Redirect(w, r, redirectURL.String(), http.StatusTemporaryRedirect)
		return
	}

	key, remainder, ok := splitEmbyProxyPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}

	item, err := a.embyServers.Get(r.Context(), key)
	if err != nil {
		handleStorageError(w, err)
		return
	}
	if item == nil || !item.Enabled {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}

	target, err := url.Parse(item.UpstreamURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("invalid upstream url for emby server %s", key))
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		applyProxyTargetPath(req, target, remainder)
		if item.APIKey != "" && req.Header.Get("X-Emby-Token") == "" && req.Header.Get("X-MediaBrowser-Token") == "" {
			req.Header.Set("X-Emby-Token", item.APIKey)
		}
		// Forward the public proxy path so upstream-aware logging/debugging can identify the route.
		req.Header.Set("X-Forwarded-Prefix", "/proxy/"+key)
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if !isEmbyPlaybackInfoPath(remainder) {
			return nil
		}
		if err := a.rewritePlaybackInfoResponse(resp, key, target); err != nil {
			log.Printf("rewrite emby playback info for %s failed: %v", key, err)
		}
		return nil
	}
	proxy.ErrorHandler = func(rw http.ResponseWriter, _ *http.Request, proxyErr error) {
		writeError(rw, http.StatusBadGateway, fmt.Sprintf("proxy emby server %s: %v", key, proxyErr))
	}
	proxy.ServeHTTP(w, r)
}

func (a *App) rewritePlaybackInfoResponse(resp *http.Response, key string, target *url.URL) error {
	if resp == nil || resp.Body == nil || resp.Request == nil {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if contentType != "" && !strings.Contains(contentType, "json") {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read playback info body: %w", err)
	}
	_ = resp.Body.Close()

	rewritten, changed, err := rewriteEmbyPlaybackInfoBody(resp.Request.Context(), body, a.rewriteManagedPlaybackPath, func(pathValue string) (string, bool, error) {
		return a.rewriteEmbyProxyURL(key, target, pathValue)
	})
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
		return err
	}
	if !changed {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
		return nil
	}

	resp.Body = io.NopCloser(bytes.NewReader(rewritten))
	resp.ContentLength = int64(len(rewritten))
	resp.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
	return nil
}

func rewriteEmbyPlaybackInfoBody(
	ctx context.Context,
	body []byte,
	rewriteManagedPath func(context.Context, string) (string, bool, error),
	rewriteURL func(string) (string, bool, error),
) ([]byte, bool, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false, fmt.Errorf("decode playback info body: %w", err)
	}

	changed := rewritePlaybackInfoMap(ctx, payload, rewriteManagedPath, rewriteURL)
	if !changed {
		return body, false, nil
	}

	rewritten, err := json.Marshal(payload)
	if err != nil {
		return nil, false, fmt.Errorf("encode playback info body: %w", err)
	}
	return rewritten, true, nil
}

func rewritePlaybackInfoMap(
	ctx context.Context,
	current map[string]any,
	rewriteManagedPath func(context.Context, string) (string, bool, error),
	rewriteURL func(string) (string, bool, error),
) bool {
	changed := false
	if changedSource := rewritePlaybackMediaSource(ctx, current, rewriteManagedPath); changedSource {
		changed = true
	}
	for key, value := range current {
		switch typed := value.(type) {
		case map[string]any:
			if rewritePlaybackInfoMap(ctx, typed, rewriteManagedPath, rewriteURL) {
				changed = true
			}
		case []any:
			if rewritePlaybackInfoList(ctx, typed, rewriteManagedPath, rewriteURL) {
				changed = true
			}
		case string:
			rewritten, ok := rewritePlaybackInfoString(key, typed, rewriteURL)
			if ok && rewritten != typed {
				current[key] = rewritten
				changed = true
			}
		}
	}
	return changed
}

func rewritePlaybackInfoList(
	ctx context.Context,
	items []any,
	rewriteManagedPath func(context.Context, string) (string, bool, error),
	rewriteURL func(string) (string, bool, error),
) bool {
	changed := false
	for _, item := range items {
		switch typed := item.(type) {
		case map[string]any:
			if rewritePlaybackInfoMap(ctx, typed, rewriteManagedPath, rewriteURL) {
				changed = true
			}
		case []any:
			if rewritePlaybackInfoList(ctx, typed, rewriteManagedPath, rewriteURL) {
				changed = true
			}
		}
	}
	return changed
}

func rewritePlaybackMediaSource(
	ctx context.Context,
	current map[string]any,
	rewriteManagedPath func(context.Context, string) (string, bool, error),
) bool {
	pathValue, _ := current["Path"].(string)
	if pathValue == "" {
		return false
	}
	rewritten, shouldRewrite, err := rewriteManagedPath(ctx, pathValue)
	if err != nil {
		log.Printf("resolve playback direct stream from path %q failed: %v", pathValue, err)
		return false
	}
	if !shouldRewrite || rewritten == "" {
		return false
	}
	current["DirectStreamUrl"] = rewritten
	return true
}

func rewritePlaybackInfoString(
	fieldName string,
	value string,
	rewriteURL func(string) (string, bool, error),
) (string, bool) {
	if value == "" {
		return value, false
	}
	if !isEmbyPlaybackURLField(fieldName) {
		return value, false
	}
	rewritten, shouldRewrite, err := rewriteURL(value)
	if err != nil {
		log.Printf("rewrite playback url field %s=%q failed: %v", fieldName, value, err)
		return value, false
	}
	return rewritten, shouldRewrite
}

func isEmbyPlaybackURLField(fieldName string) bool {
	switch fieldName {
	case "TranscodingUrl", "Url":
		return true
	default:
		return false
	}
}

func (a *App) rewriteManagedPlaybackPath(_ context.Context, pathValue string) (string, bool, error) {
	playbackURL, ok := a.parseManagedStreamURL(pathValue)
	if !ok {
		return "", false, nil
	}
	return playbackURL.String(), true, nil
}

func (a *App) rewriteEmbyProxyURL(key string, target *url.URL, pathValue string) (string, bool, error) {
	if key == "" || target == nil {
		return "", false, nil
	}
	publicBaseURL := strings.TrimSpace(a.config.Server.PublicBaseURL)
	if publicBaseURL == "" {
		return "", false, nil
	}
	publicBase, err := url.Parse(publicBaseURL)
	if err != nil || publicBase.Scheme == "" || publicBase.Host == "" {
		return "", false, nil
	}
	if rewritten, ok := a.normalizeManagedPlaybackURL(publicBase, key, pathValue); ok {
		return rewritten, true, nil
	}
	parsed, err := url.Parse(pathValue)
	if err != nil {
		return "", false, err
	}

	remainder, ok := extractEmbyProxyRemainder(parsed, target)
	if !ok {
		return "", false, nil
	}

	return buildPublicRelativeURL(publicBase, joinURLPath("/proxy/"+key, remainder), parsed.RawQuery, parsed.Fragment), true, nil
}

func (a *App) normalizeManagedPlaybackURL(publicBase *url.URL, key string, pathValue string) (string, bool) {
	if publicBase == nil || key == "" || pathValue == "" {
		return "", false
	}
	parsed, err := url.Parse(pathValue)
	if err != nil {
		return "", false
	}
	proxyPrefix := strings.TrimRight(publicBase.EscapedPath(), "/") + "/proxy/" + key
	streamPrefix := strings.TrimRight(publicBase.EscapedPath(), "/") + "/stream/"
	pathOnly := parsed.EscapedPath()
	if pathOnly == "" {
		pathOnly = parsed.Path
	}

	if parsed.IsAbs() {
		if !strings.EqualFold(parsed.Scheme, publicBase.Scheme) || !strings.EqualFold(parsed.Host, publicBase.Host) {
			return "", false
		}
		pathValue = pathOnly
	} else {
		pathValue = pathOnly
		if !strings.HasPrefix(pathValue, "/") {
			return "", false
		}
		proxyPrefix = "/proxy/" + key
		streamPrefix = "/stream/"
	}

	if pathValue == proxyPrefix || strings.HasPrefix(pathValue, proxyPrefix+"/") || strings.HasPrefix(pathValue, streamPrefix) {
		return buildPublicRelativeURL(publicBase, pathValue, parsed.RawQuery, parsed.Fragment), true
	}
	return "", false
}

func buildPublicRelativeURL(publicBase *url.URL, requestPath, rawQuery, fragment string) string {
	publicBasePath := ""
	if publicBase != nil {
		publicBasePath = strings.TrimRight(publicBase.EscapedPath(), "/")
	}
	if publicBasePath != "" {
		if requestPath == publicBasePath {
			requestPath = "/"
		} else if strings.HasPrefix(requestPath, publicBasePath+"/") {
			requestPath = "/" + strings.TrimPrefix(requestPath, publicBasePath+"/")
		}
	}
	rewritten := url.URL{
		Path:     joinURLPath(publicBase.Path, requestPath),
		RawQuery: rawQuery,
		Fragment: fragment,
	}
	rewritten.RawPath = rewritten.Path
	return rewritten.String()
}

func extractEmbyProxyRemainder(parsed *url.URL, target *url.URL) (string, bool) {
	if parsed == nil || target == nil {
		return "", false
	}
	if parsed.IsAbs() {
		if !strings.EqualFold(parsed.Scheme, target.Scheme) || !strings.EqualFold(parsed.Host, target.Host) {
			return "", false
		}
	}
	pathValue := parsed.EscapedPath()
	if pathValue == "" {
		pathValue = parsed.Path
	}
	if pathValue == "" {
		return "", false
	}
	if !strings.HasPrefix(pathValue, "/") {
		pathValue = "/" + pathValue
	}

	targetBase := strings.TrimRight(target.EscapedPath(), "/")
	if targetBase != "" {
		if pathValue == targetBase {
			return "/", true
		}
		if strings.HasPrefix(pathValue, targetBase+"/") {
			return "/" + strings.TrimPrefix(pathValue, targetBase+"/"), true
		}
	}
	return pathValue, true
}

func (a *App) parseManagedStreamURL(pathValue string) (*url.URL, bool) {
	parsed, err := url.Parse(pathValue)
	if err != nil {
		return nil, false
	}
	publicBaseURL := strings.TrimSpace(a.config.Server.PublicBaseURL)
	if publicBaseURL == "" {
		return nil, false
	}
	publicBase, err := url.Parse(publicBaseURL)
	if err != nil || publicBase.Scheme == "" || publicBase.Host == "" {
		return nil, false
	}

	if parsed.IsAbs() {
		if !strings.EqualFold(parsed.Scheme, publicBase.Scheme) || !strings.EqualFold(parsed.Host, publicBase.Host) {
			return nil, false
		}
		prefix := strings.TrimRight(publicBase.EscapedPath(), "/") + "/stream/"
		if !strings.HasPrefix(parsed.EscapedPath(), prefix) {
			return nil, false
		}
		return parsed, true
	}
	if !strings.HasPrefix(parsed.EscapedPath(), "/stream/") {
		return nil, false
	}
	resolved := *publicBase
	resolved.Path = joinURLPath(publicBase.Path, parsed.Path)
	resolved.RawPath = resolved.Path
	resolved.RawQuery = parsed.RawQuery
	resolved.Fragment = parsed.Fragment
	return &resolved, true
}

func (a *App) parseManagedStreamPath(pathValue string) (providerID string, providerPath string, ok bool) {
	parsed, ok := a.parseManagedStreamURL(pathValue)
	if !ok {
		return "", "", false
	}
	streamPath := parsed.EscapedPath()
	if publicBaseURL := strings.TrimSpace(a.config.Server.PublicBaseURL); publicBaseURL != "" {
		if publicBase, err := url.Parse(publicBaseURL); err == nil && publicBase.EscapedPath() != "" {
			prefix := strings.TrimRight(publicBase.EscapedPath(), "/") + "/stream/"
			if strings.HasPrefix(streamPath, prefix) {
				streamPath = "/stream/" + strings.TrimPrefix(streamPath, prefix)
			}
		}
	}
	parts := strings.SplitN(strings.TrimPrefix(streamPath, "/stream/"), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	providerPath, err := decodeProviderPath(parts[1])
	if err != nil {
		return "", "", false
	}
	return parts[0], providerPath, true
}

func isEmbyPlaybackInfoPath(pathValue string) bool {
	return strings.HasSuffix(strings.ToLower(pathValue), "/playbackinfo")
}

func toEmbyServerModel(payload embyServerPayload) (model.EmbyServer, error) {
	key := strings.TrimSpace(payload.Key)
	name := strings.TrimSpace(payload.Name)
	upstreamURL := strings.TrimSpace(payload.UpstreamURL)
	apiKey := strings.TrimSpace(payload.APIKey)

	if key == "" {
		return model.EmbyServer{}, fmt.Errorf("key is required")
	}
	if !embyServerKeyPattern.MatchString(key) {
		return model.EmbyServer{}, fmt.Errorf("key must be 2-63 characters and only contain letters, numbers, '.', '-', or '_'")
	}
	if name == "" {
		return model.EmbyServer{}, fmt.Errorf("name is required")
	}
	if upstreamURL == "" {
		return model.EmbyServer{}, fmt.Errorf("upstream_url is required")
	}
	parsed, err := url.Parse(upstreamURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return model.EmbyServer{}, fmt.Errorf("upstream_url must be a valid absolute url")
	}

	return model.EmbyServer{
		Key:         key,
		Name:        name,
		UpstreamURL: strings.TrimRight(upstreamURL, "/"),
		APIKey:      apiKey,
		Enabled:     payload.Enabled,
	}, nil
}

func splitEmbyProxyPath(requestPath string) (key string, remainder string, ok bool) {
	pathValue := strings.TrimPrefix(requestPath, "/proxy/")
	if pathValue == requestPath || pathValue == "" {
		return "", "", false
	}
	parts := strings.SplitN(pathValue, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "", "", false
	}
	remainder = "/"
	if len(parts) == 2 && parts[1] != "" {
		remainder = "/" + parts[1]
	}
	return parts[0], remainder, true
}

func normalizeEmbyProxyBasePath(requestPath string) (string, bool) {
	if !strings.HasPrefix(requestPath, "/proxy/") || strings.HasSuffix(requestPath, "/") {
		return "", false
	}
	pathValue := strings.TrimPrefix(requestPath, "/proxy/")
	if pathValue == "" || strings.Contains(pathValue, "/") {
		return "", false
	}
	return requestPath + "/", true
}

func applyProxyTargetPath(req *http.Request, target *url.URL, remainder string) {
	req.URL.Scheme = target.Scheme
	req.URL.Host = target.Host
	req.Host = target.Host
	req.URL.Path = joinURLPath(target.Path, remainder)
	req.URL.RawPath = req.URL.Path
	if target.RawQuery == "" || req.URL.RawQuery == "" {
		req.URL.RawQuery = target.RawQuery + req.URL.RawQuery
		return
	}
	req.URL.RawQuery = target.RawQuery + "&" + req.URL.RawQuery
}

func joinURLPath(basePath, remainder string) string {
	basePath = strings.TrimSuffix(basePath, "/")
	if remainder == "" || remainder == "/" {
		if basePath == "" {
			return "/"
		}
		return basePath + "/"
	}
	if basePath == "" {
		return remainder
	}
	return basePath + remainder
}

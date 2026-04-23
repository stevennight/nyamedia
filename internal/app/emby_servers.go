package app

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
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
	proxy.ErrorHandler = func(rw http.ResponseWriter, _ *http.Request, proxyErr error) {
		writeError(rw, http.StatusBadGateway, fmt.Sprintf("proxy emby server %s: %v", key, proxyErr))
	}
	proxy.ServeHTTP(w, r)
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

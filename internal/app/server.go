package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	config     Config
	store      *Store
	vault      *Vault
	client     *http.Client
	events     *EventHub
	active     *ActiveCaptures
	staticFS   fs.FS
	logger     *slog.Logger
	background context.CancelFunc
}

func NewServer(config Config, store *Store, vault *Vault, staticFS fs.FS, logger *slog.Logger) *Server {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = config.UpstreamHeaderTimeout
	transport.IdleConnTimeout = 90 * time.Second
	return &Server{
		config: config, store: store, vault: vault, staticFS: staticFS, logger: logger,
		client: &http.Client{
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		events: NewEventHub(), active: NewActiveCaptures(),
	}
}

func (server *Server) StartBackground(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	server.background = cancel
	go func() {
		_ = server.store.Cleanup(ctx, server.config.RetentionDays, server.config.MaxRequestsPerProject)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := server.store.Cleanup(ctx, server.config.RetentionDays, server.config.MaxRequestsPerProject); err != nil {
					server.logger.Error("retention cleanup failed", "error", err)
				}
			}
		}
	}()
}

func (server *Server) StopBackground() {
	if server.background != nil {
		server.background()
	}
}

func (server *Server) Handler() http.Handler {
	return http.HandlerFunc(server.route)
}

func (server *Server) route(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	switch {
	case request.URL.Path == "/healthz":
		if request.Method != http.MethodGet {
			server.methodNotAllowed(writer, http.MethodGet)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"status": "ok"})
	case request.URL.Path == "/v1/chat/completions" && request.Method == http.MethodPost:
		server.handleProxy(writer, request, "/chat/completions")
	case request.URL.Path == "/v1/models" && request.Method == http.MethodGet:
		server.handleProxy(writer, request, "/models")
	case strings.HasPrefix(request.URL.Path, "/v1/"):
		writeOpenAIError(writer, http.StatusNotFound, "unsupported_endpoint", "only POST /v1/chat/completions and GET /v1/models are supported")
	case strings.HasPrefix(request.URL.Path, "/api/"):
		server.routeAPI(writer, request)
	default:
		server.serveStatic(writer, request)
	}
}

func (server *Server) routeAPI(writer http.ResponseWriter, request *http.Request) {
	segments := splitPath(strings.TrimPrefix(request.URL.Path, "/api/"))
	if len(segments) == 1 && segments[0] == "projects" {
		switch request.Method {
		case http.MethodGet:
			server.listProjects(writer, request)
		case http.MethodPost:
			server.createProject(writer, request)
		default:
			server.methodNotAllowed(writer, http.MethodGet, http.MethodPost)
		}
		return
	}
	if len(segments) >= 2 && segments[0] == "projects" {
		projectID := segments[1]
		if len(segments) == 2 {
			switch request.Method {
			case http.MethodGet:
				server.getProject(writer, request, projectID)
			case http.MethodPatch:
				server.updateProject(writer, request, projectID)
			case http.MethodDelete:
				server.deleteProject(writer, request, projectID)
			default:
				server.methodNotAllowed(writer, http.MethodGet, http.MethodPatch, http.MethodDelete)
			}
			return
		}
		if len(segments) == 3 {
			switch segments[2] {
			case "reveal-key":
				server.requireMethod(writer, request, http.MethodPost, func() { server.revealProjectKey(writer, request, projectID) })
			case "rotate-key":
				server.requireMethod(writer, request, http.MethodPost, func() { server.rotateProjectKey(writer, request, projectID) })
			case "test-upstream":
				server.requireMethod(writer, request, http.MethodPost, func() { server.testUpstream(writer, request, projectID) })
			case "requests":
				switch request.Method {
				case http.MethodGet:
					server.listRequests(writer, request, projectID)
				case http.MethodDelete:
					server.clearRequests(writer, request, projectID)
				default:
					server.methodNotAllowed(writer, http.MethodGet, http.MethodDelete)
				}
			case "capture-groups":
				switch request.Method {
				case http.MethodGet:
					server.listCaptureGroups(writer, request, projectID)
				case http.MethodPost:
					server.saveCaptureGroup(writer, request, projectID)
				default:
					server.methodNotAllowed(writer, http.MethodGet, http.MethodPost)
				}
			default:
				writeAPIError(writer, http.StatusNotFound, "not found")
			}
			return
		}
		if len(segments) == 4 && segments[2] == "capture" {
			switch segments[3] {
			case "start":
				server.requireMethod(writer, request, http.MethodPost, func() { server.startCapture(writer, request, projectID) })
			case "pause":
				server.requireMethod(writer, request, http.MethodPost, func() { server.pauseCapture(writer, request, projectID) })
			default:
				writeAPIError(writer, http.StatusNotFound, "not found")
			}
			return
		}
		if len(segments) == 4 && segments[2] == "requests" && segments[3] == "batch-delete" {
			server.requireMethod(writer, request, http.MethodPost, func() { server.deleteRequests(writer, request, projectID) })
			return
		}
	}
	if len(segments) == 2 && segments[0] == "requests" {
		server.requireMethod(writer, request, http.MethodGet, func() { server.getRequest(writer, request, segments[1]) })
		return
	}
	if len(segments) == 1 && segments[0] == "events" {
		server.requireMethod(writer, request, http.MethodGet, func() { server.liveEvents(writer, request) })
		return
	}
	writeAPIError(writer, http.StatusNotFound, "not found")
}

func (server *Server) serveStatic(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		server.methodNotAllowed(writer, http.MethodGet, http.MethodHead)
		return
	}
	requested := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
	if requested == "." || requested == "" {
		requested = "index.html"
	}
	content, err := fs.ReadFile(server.staticFS, requested)
	if err != nil {
		if strings.HasPrefix(requested, "assets/") {
			http.NotFound(writer, request)
			return
		}
		content, err = fs.ReadFile(server.staticFS, "index.html")
		requested = "index.html"
	}
	if err != nil {
		http.Error(writer, "frontend assets are unavailable", http.StatusServiceUnavailable)
		return
	}
	if contentType := mime.TypeByExtension(path.Ext(requested)); contentType != "" {
		writer.Header().Set("Content-Type", contentType)
	}
	if strings.HasPrefix(requested, "assets/") {
		writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		writer.Header().Set("Cache-Control", "no-cache")
	}
	writer.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = writer.Write(content)
	}
}

func (server *Server) liveEvents(writer http.ResponseWriter, request *http.Request) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeAPIError(writer, http.StatusInternalServerError, "streaming is unavailable")
		return
	}
	projectID := request.URL.Query().Get("project_id")
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte("event: ready\ndata: {}\n\n"))
	flusher.Flush()
	subscriptionID, events := server.events.Subscribe()
	defer server.events.Unsubscribe(subscriptionID)
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-keepAlive.C:
			_, _ = writer.Write([]byte(": keepalive\n\n"))
			flusher.Flush()
		case event := <-events:
			if projectID != "" && projectID != event.ProjectID {
				continue
			}
			_, _ = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event.Type, event.JSON())
			flusher.Flush()
		}
	}
}

func splitPath(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(strings.Trim(value, "/"), "/")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func (server *Server) requireMethod(writer http.ResponseWriter, request *http.Request, method string, handler func()) {
	if request.Method != method {
		server.methodNotAllowed(writer, method)
		return
	}
	handler()
}

func (server *Server) methodNotAllowed(writer http.ResponseWriter, methods ...string) {
	writer.Header().Set("Allow", strings.Join(methods, ", "))
	writeAPIError(writer, http.StatusMethodNotAllowed, "method not allowed")
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeAPIError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]any{"error": map[string]any{"message": message}})
}

func writeOpenAIError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, map[string]any{"error": map[string]any{
		"message": message, "type": "proxy_error", "param": nil, "code": code,
	}})
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func parseLimit(value string) int {
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return 50
	}
	return limit
}

func handleStoreError(writer http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		writeAPIError(writer, http.StatusNotFound, "not found")
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
		writeAPIError(writer, http.StatusConflict, "a project with that name already exists")
		return
	}
	if errors.Is(err, ErrCaptureNotPaused) || errors.Is(err, ErrCaptureSessionGone) {
		writeAPIError(writer, http.StatusConflict, err.Error())
		return
	}
	writeAPIError(writer, http.StatusInternalServerError, "internal server error")
}

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type projectInput struct {
	Name           string  `json:"name"`
	BaseURL        string  `json:"baseUrl"`
	UpstreamAPIKey *string `json:"upstreamApiKey,omitempty"`
}

type captureGroupInput struct {
	Name string `json:"name"`
}

func (server *Server) listProjects(writer http.ResponseWriter, request *http.Request) {
	projects, err := server.store.ListProjects(request.Context())
	if err != nil {
		handleStoreError(writer, err)
		return
	}
	for index := range projects {
		server.decorateProject(&projects[index])
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": projects})
}

func (server *Server) createProject(writer http.ResponseWriter, request *http.Request) {
	var input projectInput
	if !decodeJSON(writer, request, &input) {
		return
	}
	name, baseURL, err := validateProjectInput(input.Name, input.BaseURL)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, err.Error())
		return
	}
	upstreamKey := ""
	if input.UpstreamAPIKey != nil {
		upstreamKey = strings.TrimSpace(*input.UpstreamAPIKey)
	}
	upstreamEncrypted, err := server.vault.Encrypt(upstreamKey)
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "could not encrypt upstream key")
		return
	}
	projectKey, err := GenerateProjectKey()
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "could not generate project key")
		return
	}
	projectKeyEncrypted, err := server.vault.Encrypt(projectKey)
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "could not encrypt project key")
		return
	}
	id, err := NewID("prj")
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "could not generate project id")
		return
	}
	project, err := server.store.CreateProject(request.Context(), CreateProjectParams{
		ID: id, Name: name, BaseURL: baseURL, UpstreamKeyEncrypted: upstreamEncrypted,
		APIKeyHash: HashProjectKey(projectKey), APIKeyEncrypted: projectKeyEncrypted,
		APIKeyPrefix: projectKeyPrefix(projectKey),
	})
	if err != nil {
		handleStoreError(writer, err)
		return
	}
	server.decorateProject(&project)
	writer.Header().Set("Cache-Control", "no-store")
	server.events.Publish(LiveEvent{Type: "project_changed", ProjectID: project.ID})
	writeJSON(writer, http.StatusCreated, map[string]any{"project": project, "apiKey": projectKey})
}

func (server *Server) getProject(writer http.ResponseWriter, request *http.Request, id string) {
	project, err := server.store.GetProject(request.Context(), id)
	if err != nil {
		handleStoreError(writer, err)
		return
	}
	server.decorateProject(&project)
	writeJSON(writer, http.StatusOK, project)
}

func (server *Server) updateProject(writer http.ResponseWriter, request *http.Request, id string) {
	var input projectInput
	if !decodeJSON(writer, request, &input) {
		return
	}
	params := UpdateProjectParams{}
	if input.Name != "" {
		name := strings.TrimSpace(input.Name)
		if name == "" || len(name) > 100 {
			writeAPIError(writer, http.StatusBadRequest, "project name must contain 1-100 characters")
			return
		}
		params.Name = &name
	}
	if input.BaseURL != "" {
		baseURL, err := normalizeBaseURL(input.BaseURL)
		if err != nil {
			writeAPIError(writer, http.StatusBadRequest, err.Error())
			return
		}
		params.BaseURL = &baseURL
	}
	if input.UpstreamAPIKey != nil {
		encrypted, err := server.vault.Encrypt(strings.TrimSpace(*input.UpstreamAPIKey))
		if err != nil {
			writeAPIError(writer, http.StatusInternalServerError, "could not encrypt upstream key")
			return
		}
		params.UpstreamKeyEncrypted = &encrypted
	}
	project, err := server.store.UpdateProject(request.Context(), id, params)
	if err != nil {
		handleStoreError(writer, err)
		return
	}
	server.decorateProject(&project)
	server.events.Publish(LiveEvent{Type: "project_changed", ProjectID: project.ID})
	writeJSON(writer, http.StatusOK, project)
}

func (server *Server) deleteProject(writer http.ResponseWriter, request *http.Request, id string) {
	if err := server.store.DeleteProject(request.Context(), id); err != nil {
		handleStoreError(writer, err)
		return
	}
	server.events.Publish(LiveEvent{Type: "project_changed", ProjectID: id})
	writeJSON(writer, http.StatusOK, map[string]any{"deleted": true})
}

func (server *Server) revealProjectKey(writer http.ResponseWriter, request *http.Request, id string) {
	project, err := server.store.GetProject(request.Context(), id)
	if err != nil {
		handleStoreError(writer, err)
		return
	}
	key, err := server.vault.Decrypt(project.ProjectKeyEncrypted)
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "could not decrypt project key")
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, map[string]any{"apiKey": key})
}

func (server *Server) rotateProjectKey(writer http.ResponseWriter, request *http.Request, id string) {
	key, err := GenerateProjectKey()
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "could not generate project key")
		return
	}
	encrypted, err := server.vault.Encrypt(key)
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "could not encrypt project key")
		return
	}
	if err := server.store.RotateProjectKey(request.Context(), id, HashProjectKey(key), encrypted, projectKeyPrefix(key)); err != nil {
		handleStoreError(writer, err)
		return
	}
	server.events.Publish(LiveEvent{Type: "project_changed", ProjectID: id})
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, map[string]any{"apiKey": key})
}

func (server *Server) testUpstream(writer http.ResponseWriter, request *http.Request, id string) {
	project, err := server.store.GetProject(request.Context(), id)
	if err != nil {
		handleStoreError(writer, err)
		return
	}
	upstreamURL, err := joinUpstreamURL(project.BaseURL, "/models", "")
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, err.Error())
		return
	}
	upstreamKey, err := server.vault.Decrypt(project.UpstreamKeyEncrypted)
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "could not decrypt upstream key")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), minDuration(server.config.UpstreamHeaderTimeout, 30*time.Second))
	defer cancel()
	upstreamRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	upstreamRequest.Header.Set("Accept", "application/json")
	upstreamRequest.Header.Set("Accept-Encoding", "identity")
	if upstreamKey != "" {
		upstreamRequest.Header.Set("Authorization", "Bearer "+upstreamKey)
	}
	started := time.Now()
	response, err := server.client.Do(upstreamRequest)
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error(), "durationMs": time.Since(started).Milliseconds()})
		return
	}
	response.Body.Close()
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok":     response.StatusCode >= 200 && response.StatusCode < 400,
		"status": response.StatusCode, "durationMs": time.Since(started).Milliseconds(),
	})
}

func (server *Server) listRequests(writer http.ResponseWriter, request *http.Request, projectID string) {
	streaming, err := parseOptionalBool(request.URL.Query().Get("streaming"))
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, err.Error())
		return
	}
	result, err := server.store.ListRequests(request.Context(), ListRequestsParams{
		ProjectID: projectID, Limit: parseLimit(request.URL.Query().Get("limit")),
		Cursor: request.URL.Query().Get("cursor"), Status: request.URL.Query().Get("status"),
		Path: request.URL.Query().Get("path"), Model: request.URL.Query().Get("model"), Streaming: streaming,
		GroupID: request.URL.Query().Get("groupId"),
	})
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (server *Server) startCapture(writer http.ResponseWriter, request *http.Request, projectID string) {
	sessionID, err := NewID("cap")
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "could not create capture session")
		return
	}
	project, err := server.store.StartCapture(request.Context(), projectID, sessionID)
	if err != nil {
		handleStoreError(writer, err)
		return
	}
	server.decorateProject(&project)
	server.events.Publish(LiveEvent{Type: "capture_changed", ProjectID: projectID})
	writeJSON(writer, http.StatusOK, project)
}

func (server *Server) pauseCapture(writer http.ResponseWriter, request *http.Request, projectID string) {
	project, err := server.store.PauseCapture(request.Context(), projectID)
	if err != nil {
		handleStoreError(writer, err)
		return
	}
	server.decorateProject(&project)
	server.events.Publish(LiveEvent{Type: "capture_changed", ProjectID: projectID})
	writeJSON(writer, http.StatusOK, project)
}

func (server *Server) listCaptureGroups(writer http.ResponseWriter, request *http.Request, projectID string) {
	groups, err := server.store.ListCaptureGroups(request.Context(), projectID)
	if err != nil {
		handleStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": groups})
}

func (server *Server) saveCaptureGroup(writer http.ResponseWriter, request *http.Request, projectID string) {
	var input captureGroupInput
	if !decodeJSON(writer, request, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 100 {
		writeAPIError(writer, http.StatusBadRequest, "group name must contain 1-100 characters")
		return
	}
	groupID, err := NewID("grp")
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "could not create capture group")
		return
	}
	group, err := server.store.SaveCaptureGroup(request.Context(), projectID, groupID, name)
	if err != nil {
		handleStoreError(writer, err)
		return
	}
	server.events.Publish(LiveEvent{Type: "capture_changed", ProjectID: projectID})
	writeJSON(writer, http.StatusCreated, group)
}

func (server *Server) getRequest(writer http.ResponseWriter, request *http.Request, id string) {
	log, err := server.store.GetRequest(request.Context(), id)
	if err != nil {
		handleStoreError(writer, err)
		return
	}
	if capture, ok := server.active.Get(id); ok {
		requestBody, requestBytes, requestTruncated := capture.Request.Snapshot()
		responseBody, responseBytes, responseTruncated := capture.Response.Snapshot()
		log.RequestBody, log.RequestBytes, log.RequestTruncated = string(requestBody), requestBytes, requestTruncated
		log.ResponseBody, log.ResponseBytes, log.ResponseTruncated = string(responseBody), responseBytes, responseTruncated
		log.Live = true
	}
	writeJSON(writer, http.StatusOK, log)
}

func (server *Server) clearRequests(writer http.ResponseWriter, request *http.Request, projectID string) {
	deleted, err := server.store.ClearProjectRequests(request.Context(), projectID)
	if err != nil {
		handleStoreError(writer, err)
		return
	}
	server.events.Publish(LiveEvent{Type: "requests_cleared", ProjectID: projectID})
	writeJSON(writer, http.StatusOK, map[string]any{"deleted": deleted})
}

func (server *Server) decorateProject(project *Project) {
	if project.UpstreamKeyEncrypted == "" {
		project.UpstreamAPIKeyMasked = ""
		return
	}
	if key, err := server.vault.Decrypt(project.UpstreamKeyEncrypted); err == nil {
		project.UpstreamAPIKeyMasked = MaskSecret(key)
	} else {
		project.UpstreamAPIKeyMasked = "••••无法解密"
	}
}

func validateProjectInput(name, rawBaseURL string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return "", "", fmt.Errorf("project name must contain 1-100 characters")
	}
	baseURL, err := normalizeBaseURL(rawBaseURL)
	return name, baseURL, err
}

func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("baseUrl must be an absolute HTTP or HTTPS URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("baseUrl must use http or https")
	}
	if parsed.Fragment != "" {
		return "", fmt.Errorf("baseUrl cannot contain a fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func joinUpstreamURL(baseURL, endpoint, rawQuery string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + endpoint
	if rawQuery != "" {
		if parsed.RawQuery == "" {
			parsed.RawQuery = rawQuery
		} else {
			parsed.RawQuery += "&" + rawQuery
		}
	}
	return parsed.String(), nil
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func marshalRaw(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func isNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

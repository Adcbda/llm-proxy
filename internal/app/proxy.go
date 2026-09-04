package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var hopByHopHeaders = map[string]struct{}{
	"Connection": {}, "Proxy-Connection": {}, "Keep-Alive": {}, "Proxy-Authenticate": {},
	"Proxy-Authorization": {}, "Te": {}, "Trailer": {}, "Transfer-Encoding": {}, "Upgrade": {},
}

var sensitiveHeaders = map[string]struct{}{
	"Authorization": {}, "Proxy-Authorization": {}, "Api-Key": {}, "X-Api-Key": {},
	"Cookie": {}, "Set-Cookie": {},
}

type captureReadCloser struct {
	reader io.Reader
	closer io.Closer
}

func (body *captureReadCloser) Read(data []byte) (int, error) { return body.reader.Read(data) }
func (body *captureReadCloser) Close() error                  { return body.closer.Close() }

func (server *Server) handleProxy(writer http.ResponseWriter, request *http.Request, endpoint string) {
	projectKey := bearerToken(request.Header.Get("Authorization"))
	if projectKey == "" {
		writeOpenAIError(writer, http.StatusUnauthorized, "invalid_api_key", "missing or invalid project API key")
		return
	}
	project, err := server.store.GetProjectByKeyHash(request.Context(), HashProjectKey(projectKey))
	if err != nil {
		writeOpenAIError(writer, http.StatusUnauthorized, "invalid_api_key", "missing or invalid project API key")
		return
	}
	upstreamKey, err := server.vault.Decrypt(project.UpstreamKeyEncrypted)
	if err != nil {
		writeOpenAIError(writer, http.StatusInternalServerError, "credential_error", "could not decrypt the project's upstream API key")
		return
	}
	upstreamURL, err := joinUpstreamURL(project.BaseURL, endpoint, request.URL.RawQuery)
	if err != nil {
		writeOpenAIError(writer, http.StatusInternalServerError, "invalid_upstream_url", "the project's upstream URL is invalid")
		return
	}
	requestID, err := NewID("req")
	if err != nil {
		writeOpenAIError(writer, http.StatusInternalServerError, "internal_error", "could not create request id")
		return
	}
	startedAt := time.Now().UTC()
	if err := server.store.StartRequest(request.Context(), StartRequestParams{
		ID: requestID, ProjectID: project.ID, Method: request.Method, Path: request.URL.Path,
		UpstreamURL: upstreamURL, RequestHeaders: redactHeaders(request.Header), StartedAt: startedAt,
	}); err != nil {
		server.logger.Error("start request capture", "error", err, "request_id", requestID)
		writeOpenAIError(writer, http.StatusInternalServerError, "capture_error", "could not start request capture")
		return
	}

	requestCapture := NewLimitedCapture(server.config.CaptureMaxBytes)
	responseCapture := NewLimitedCapture(server.config.CaptureMaxBytes)
	server.active.Put(requestID, &ActiveCapture{ProjectID: project.ID, Request: requestCapture, Response: responseCapture})
	server.events.Publish(LiveEvent{Type: "request_started", ProjectID: project.ID, RequestID: requestID})
	defer server.active.Delete(requestID)

	outgoing := request.Clone(request.Context())
	parsedURL, _ := url.Parse(upstreamURL)
	outgoing.URL = parsedURL
	outgoing.RequestURI = ""
	outgoing.Host = parsedURL.Host
	outgoing.Header = request.Header.Clone()
	stripHopHeaders(outgoing.Header)
	outgoing.Header.Del("Authorization")
	outgoing.Header.Set("Accept-Encoding", "identity")
	if upstreamKey != "" {
		outgoing.Header.Set("Authorization", "Bearer "+upstreamKey)
	}
	if request.Body != nil {
		outgoing.Body = &captureReadCloser{reader: io.TeeReader(request.Body, requestCapture), closer: request.Body}
	}

	response, doErr := server.client.Do(outgoing)
	requestBody, requestBytes, requestTruncated := requestCapture.Snapshot()
	model, streaming := parseRequestMeta(requestBody)
	if request.URL.Path == "/v1/models" {
		streaming = false
	}
	if err := server.store.UpdateRequestCapture(context.Background(), requestID, model, streaming, requestBody, requestBytes, requestTruncated); err != nil {
		server.logger.Error("save request body", "error", err, "request_id", requestID)
	}

	if doErr != nil {
		server.finishUpstreamFailure(writer, request, project.ID, requestID, startedAt, doErr)
		return
	}
	defer response.Body.Close()
	responseHeaders := redactHeaders(response.Header)
	copyHeaders(writer.Header(), response.Header)
	writer.Header().Set("X-LLM-Proxy-Request-ID", requestID)
	writer.WriteHeader(response.StatusCode)

	var aggregator *SSEAggregator
	if streaming || strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		aggregator = NewSSEAggregator()
	}
	doneForwarded, copyErr := server.copyResponse(writer, response.Body, responseCapture, aggregator, project.ID, requestID)
	if aggregator != nil {
		aggregator.Finish()
	}
	responseBody, responseBytes, responseTruncated := responseCapture.Snapshot()
	finishedAt := time.Now().UTC()
	status := "completed"
	errorMessage := ""
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		status = "upstream_error"
	}
	if copyErr != nil {
		if doneForwarded && response.StatusCode >= 200 && response.StatusCode < 300 {
			// Clients may cancel the HTTP body after consuming the SSE completion
			// sentinel. Keep the transport diagnostic without failing the request.
			server.logger.Info("stream connection closed after completion", "request_id", requestID, "error", copyErr)
		} else {
			errorMessage = copyErr.Error()
			if status != "upstream_error" && (request.Context().Err() != nil || errors.Is(copyErr, context.Canceled)) {
				status = "interrupted"
			} else {
				status = "upstream_error"
			}
		}
	}
	aggregated := ""
	if aggregator != nil {
		aggregated = aggregator.JSON()
	}
	httpStatus := response.StatusCode
	if err := server.store.FinishRequest(context.Background(), FinishRequestParams{
		ID: requestID, Status: status, HTTPStatus: &httpStatus, Error: errorMessage,
		FinishedAt: finishedAt, DurationMS: finishedAt.Sub(startedAt).Milliseconds(),
		ResponseHeaders: responseHeaders, ResponseBody: responseBody, AggregatedResponse: aggregated,
		ResponseTruncated: responseTruncated, ResponseBytes: responseBytes,
	}); err != nil {
		server.logger.Error("finish request capture", "error", err, "request_id", requestID)
	}
	server.events.Publish(LiveEvent{Type: "request_completed", ProjectID: project.ID, RequestID: requestID})
}

func (server *Server) copyResponse(writer http.ResponseWriter, source io.Reader, capture *LimitedCapture, aggregator *SSEAggregator, projectID, requestID string) (bool, error) {
	buffer := make([]byte, 32*1024)
	_, canFlush := writer.(http.Flusher)
	controller := http.NewResponseController(writer)
	doneForwarded := false
	lastProgress := time.Now()
	for {
		read, readErr := source.Read(buffer)
		if read > 0 {
			chunk := buffer[:read]
			_, _ = capture.Write(chunk)
			if aggregator != nil {
				aggregator.Feed(chunk)
			}
			written, writeErr := writer.Write(chunk)
			if writeErr != nil {
				return doneForwarded, writeErr
			}
			if written != len(chunk) {
				return doneForwarded, io.ErrShortWrite
			}
			if aggregator != nil && canFlush {
				if flushErr := controller.Flush(); flushErr != nil {
					return doneForwarded, flushErr
				}
				doneForwarded = aggregator.Done()
			}
			if time.Since(lastProgress) >= 500*time.Millisecond {
				server.events.Publish(LiveEvent{Type: "request_progress", ProjectID: projectID, RequestID: requestID})
				lastProgress = time.Now()
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return doneForwarded, nil
			}
			return doneForwarded, readErr
		}
	}
}

func (server *Server) finishUpstreamFailure(writer http.ResponseWriter, request *http.Request, projectID, requestID string, startedAt time.Time, upstreamErr error) {
	finishedAt := time.Now().UTC()
	status := "upstream_error"
	if request.Context().Err() != nil {
		status = "interrupted"
	}
	body := marshalOpenAIError("upstream_unavailable", "could not reach upstream: "+upstreamErr.Error())
	writer.Header().Set("X-LLM-Proxy-Request-ID", requestID)
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(http.StatusBadGateway)
	_, _ = writer.Write(body)
	httpStatus := http.StatusBadGateway
	_ = server.store.FinishRequest(context.Background(), FinishRequestParams{
		ID: requestID, Status: status, HTTPStatus: &httpStatus, Error: upstreamErr.Error(),
		FinishedAt: finishedAt, DurationMS: finishedAt.Sub(startedAt).Milliseconds(),
		ResponseHeaders: jsonObject{"Content-Type": {"application/json; charset=utf-8"}},
		ResponseBody:    body, ResponseBytes: int64(len(body)),
	})
	server.events.Publish(LiveEvent{Type: "request_completed", ProjectID: projectID, RequestID: requestID})
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func parseRequestMeta(body []byte) (string, bool) {
	var payload struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return "", false
	}
	return payload.Model, payload.Stream
}

func redactHeaders(headers http.Header) jsonObject {
	result := make(jsonObject, len(headers))
	for name, values := range headers {
		canonical := http.CanonicalHeaderKey(name)
		if _, sensitive := sensitiveHeaders[canonical]; sensitive {
			result[canonical] = []string{"[REDACTED]"}
		} else {
			result[canonical] = append([]string(nil), values...)
		}
	}
	return result
}

func stripHopHeaders(headers http.Header) {
	if connection := headers.Get("Connection"); connection != "" {
		for _, name := range strings.Split(connection, ",") {
			headers.Del(strings.TrimSpace(name))
		}
	}
	for name := range hopByHopHeaders {
		headers.Del(name)
	}
}

func copyHeaders(destination, source http.Header) {
	for name, values := range source {
		if _, hop := hopByHopHeaders[http.CanonicalHeaderKey(name)]; hop {
			continue
		}
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}

func marshalOpenAIError(code, message string) []byte {
	value := map[string]any{"error": map[string]any{
		"message": message, "type": "proxy_error", "param": nil, "code": code,
	}}
	data, _ := json.Marshal(value)
	return append(data, '\n')
}

func proxyError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func TestProxyClientClosesStream(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		finishReason string
		sentinel     string
		httpStatus   int
		captureLimit int64
		wantStatus   string
	}{
		{"after_done", "stop", "data: [DONE]\n\n", 200, 1 << 20, "completed"},
		{"after_tool_calls", "tool_calls", "data: [DONE]\r\n\r\n", 200, 1 << 20, "completed"},
		{"truncated_capture", "stop", "data: [DONE]\n\n", 200, 32, "completed"},
		{"before_done", "stop", "", 200, 1 << 20, "interrupted"},
		{"partial_done", "stop", "data: [DONE]\n", 200, 1 << 20, "interrupted"},
		{"http_error", "stop", "data: [DONE]\n\n", 500, 1 << 20, "upstream_error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload := fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"},\"finish_reason\":%q}]}\n\n", test.finishReason) +
				"data: {\"choices\":[],\"usage\":{\"total_tokens\":12}}\n\n" + test.sentinel
			release := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_, _ = io.Copy(io.Discard, request.Body)
				writer.Header().Set("Content-Type", "text/event-stream")
				writer.WriteHeader(test.httpStatus)
				_, _ = io.WriteString(writer, payload)
				writer.(http.Flusher).Flush()
				// Keep HTTP open after [DONE], like the upstream in the incident.
				select {
				case <-request.Context().Done():
				case <-release:
				}
			}))
			defer upstream.Close()
			defer close(release)
			server, store, key := newTestServer(t, upstream.URL, "", test.captureLimit)
			handled := make(chan struct{})
			proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				defer close(handled)
				server.Handler().ServeHTTP(writer, request)
			}))
			defer proxy.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, proxy.URL+"/v1/chat/completions", strings.NewReader(`{"model":"test","stream":true,"messages":[]}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+key)
			response, err := proxy.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			received := make([]byte, len(payload))
			if _, err := io.ReadFull(response.Body, received); err != nil {
				t.Fatal(err)
			}
			if string(received) != payload {
				t.Fatal("proxy modified the SSE response")
			}
			// A client that has received [DONE] stops reading before HTTP EOF.
			response.Body.Close()
			cancel()
			select {
			case <-handled:
			case <-time.After(5 * time.Second):
				t.Fatal("proxy did not finish after client cancellation")
			}
			log, err := store.GetRequest(context.Background(), response.Header.Get("X-LLM-Proxy-Request-ID"))
			if err != nil {
				t.Fatal(err)
			}
			if log.Status != test.wantStatus || log.HTTPStatus == nil || *log.HTTPStatus != test.httpStatus {
				t.Fatalf("status = %q, HTTP = %v, error = %q; want %q", log.Status, log.HTTPStatus, log.Error, test.wantStatus)
			}
			if (log.Error == "") != (test.wantStatus == "completed") {
				t.Fatalf("unexpected error for %s: %q", log.Status, log.Error)
			}
			if log.ResponseBytes != int64(len(payload)) || log.ResponseTruncated != (int64(len(payload)) > test.captureLimit) {
				t.Fatal("incorrect capture byte count or truncation flag")
			}
			if !strings.Contains(log.AggregatedResponse, `"total_tokens":12`) {
				t.Fatal("usage was not retained")
			}
		})
	}
}

type streamErrorWriter struct {
	*httptest.ResponseRecorder
	writeErr error
	flushErr error
	short    bool
}

func (writer *streamErrorWriter) Write(data []byte) (int, error) {
	if writer.writeErr != nil {
		return 0, writer.writeErr
	}
	if writer.short {
		return len(data) - 1, nil
	}
	return writer.ResponseRecorder.Write(data)
}

func (writer *streamErrorWriter) FlushError() error { return writer.flushErr }

func TestProxyCompletionRequiresSuccessfulWriteAndFlush(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		writeErr error
		flushErr error
		short    bool
		wantDone bool
		wantErr  error
	}{
		{"cancel_after_flush", nil, nil, false, true, context.Canceled},
		{"upstream_close_after_flush", nil, nil, false, true, io.ErrUnexpectedEOF},
		{"write_failure", io.ErrClosedPipe, nil, false, false, io.ErrClosedPipe},
		{"short_write", nil, nil, true, false, io.ErrShortWrite},
		{"flush_failure", nil, io.ErrClosedPipe, false, false, io.ErrClosedPipe},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := &streamErrorWriter{httptest.NewRecorder(), test.writeErr, test.flushErr, test.short}
			source := io.MultiReader(strings.NewReader("data: [DONE]\n\n"), iotest.ErrReader(test.wantErr))
			server := &Server{events: NewEventHub()}
			done, err := server.copyResponse(writer, source, NewLimitedCapture(1024), NewSSEAggregator(), "project", "request")
			if done != test.wantDone || !errors.Is(err, test.wantErr) {
				t.Fatalf("copyResponse = (%v, %v), want (%v, %v)", done, err, test.wantDone, test.wantErr)
			}
		})
	}
}

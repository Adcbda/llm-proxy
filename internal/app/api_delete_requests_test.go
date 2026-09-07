package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBatchDeleteRequests(t *testing.T) {
	t.Parallel()
	server, store, _ := newTestServer(t, "http://upstream.test/v1", "", 1<<20)
	startedAt := time.Now().UTC()
	for _, id := range []string{"req_delete_a", "req_delete_b", "req_keep", "req_running"} {
		if err := store.StartRequest(context.Background(), StartRequestParams{
			ID: id, ProjectID: "prj_test", Method: http.MethodPost, Path: "/v1/chat/completions",
			UpstreamURL: "http://upstream.test/v1/chat/completions", StartedAt: startedAt,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"req_delete_a", "req_delete_b", "req_keep"} {
		if err := store.FinishRequest(context.Background(), FinishRequestParams{
			ID: id, Status: "completed", FinishedAt: startedAt.Add(time.Millisecond), DurationMS: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/projects/prj_test/requests/batch-delete",
		strings.NewReader(`{"ids":["req_delete_a","req_delete_b","req_delete_a","req_running","req_missing"]}`))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("batch delete returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Deleted int64 `json:"deleted"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Deleted != 2 {
		t.Fatalf("deleted %d requests, want 2", response.Deleted)
	}
	for _, id := range []string{"req_delete_a", "req_delete_b"} {
		if _, err := store.GetRequest(context.Background(), id); !isNotFound(err) {
			t.Fatalf("selected completed request %s still exists: %v", id, err)
		}
	}
	for _, id := range []string{"req_keep", "req_running"} {
		if _, err := store.GetRequest(context.Background(), id); err != nil {
			t.Fatalf("request %s should have been preserved: %v", id, err)
		}
	}
}

func TestBatchDeleteRequestsRequiresIDs(t *testing.T) {
	t.Parallel()
	server, _, _ := newTestServer(t, "http://upstream.test/v1", "", 1<<20)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/projects/prj_test/requests/batch-delete", strings.NewReader(`{"ids":[]}`))
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("empty batch returned %d: %s", recorder.Code, recorder.Body.String())
	}
}

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

func TestClearCurrentCaptureRequests(t *testing.T) {
	t.Parallel()
	server, store, _ := newTestServer(t, "http://upstream.test/v1", "", 1<<20)
	project, err := store.GetProject(context.Background(), "prj_test")
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	requests := []StartRequestParams{
		{ID: "req_current_done", CaptureSessionID: project.CaptureSessionID},
		{ID: "req_current_running", CaptureSessionID: project.CaptureSessionID},
		{ID: "req_previous", CaptureSessionID: "cap_previous"},
	}
	for _, params := range requests {
		params.ProjectID = project.ID
		params.Method = http.MethodPost
		params.Path = "/v1/chat/completions"
		params.UpstreamURL = "http://upstream.test/v1/chat/completions"
		params.StartedAt = startedAt
		if err := store.StartRequest(context.Background(), params); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"req_current_done", "req_previous"} {
		if err := store.FinishRequest(context.Background(), FinishRequestParams{
			ID: id, Status: "completed", FinishedAt: startedAt.Add(time.Millisecond), DurationMS: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.ExecContext(context.Background(), `INSERT INTO capture_groups
		(id, project_id, session_id, name, started_at, ended_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, "grp_previous", project.ID, "cap_previous", "历史分组",
		formatTime(startedAt), formatTime(startedAt.Add(time.Millisecond)), formatTime(startedAt)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(context.Background(), `UPDATE request_logs SET capture_group_id = ? WHERE id = ?`, "grp_previous", "req_previous"); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/projects/prj_test/capture/clear", nil)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("clear current capture returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Deleted int64 `json:"deleted"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Deleted != 1 {
		t.Fatalf("deleted %d requests, want 1", response.Deleted)
	}
	if _, err := store.GetRequest(context.Background(), "req_current_done"); !isNotFound(err) {
		t.Fatalf("finished request from current capture still exists: %v", err)
	}
	for _, id := range []string{"req_current_running", "req_previous"} {
		if _, err := store.GetRequest(context.Background(), id); err != nil {
			t.Fatalf("request %s should have been preserved: %v", id, err)
		}
	}
	groups, err := store.ListCaptureGroups(context.Background(), project.ID)
	if err != nil || len(groups) != 1 || groups[0].ID != "grp_previous" || groups[0].RequestCount != 1 {
		t.Fatalf("historical capture group changed: %+v, %v", groups, err)
	}
	project, err = store.GetProject(context.Background(), project.ID)
	if err != nil || project.CaptureState != "capturing" || project.CaptureRequestCount != 1 {
		t.Fatalf("capture state changed after clear: %+v, %v", project, err)
	}
}

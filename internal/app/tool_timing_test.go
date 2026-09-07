package app

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestEstimateToolTimingMatchesAdjacentToolRound(t *testing.T) {
	t.Parallel()
	_, store, _ := newTestServer(t, "http://127.0.0.1:1", "", 1024)
	project, err := store.GetProject(context.Background(), "prj_test")
	if err != nil {
		t.Fatal(err)
	}
	previousStarted := time.Now().UTC().Add(-2 * time.Second)
	previousFinished := previousStarted.Add(500 * time.Millisecond)
	startCapturedRequest(t, store, "req_previous", project.CaptureSessionID, previousStarted,
		`{"model":"test","messages":[{"role":"user","content":"search"}]}`)
	finishCapturedRequest(t, store, "req_previous", previousFinished,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_a","type":"function","function":{"name":"search","arguments":"{}"}},{"id":"call_b","type":"function","function":{"name":"fetch","arguments":"{}"}}]}}]}`)

	currentStarted := previousFinished.Add(875 * time.Millisecond)
	startCapturedRequest(t, store, "req_current", project.CaptureSessionID, currentStarted,
		`{"model":"test","messages":[{"role":"assistant","tool_calls":[{"id":"call_a"},{"id":"call_b"}]},{"role":"tool","tool_call_id":"call_a","content":"one"},{"role":"tool","tool_call_id":"call_b","content":"two"}]}`)
	current, err := store.GetRequest(context.Background(), "req_current")
	if err != nil {
		t.Fatal(err)
	}
	estimate, err := store.EstimateToolTiming(context.Background(), current)
	if err != nil {
		t.Fatal(err)
	}
	if estimate == nil || estimate.DurationMS != 875 || estimate.PreviousRequestID != "req_previous" {
		t.Fatalf("unexpected estimate: %+v", estimate)
	}
	if len(estimate.ToolCallIDs) != 2 || estimate.ToolCallIDs[0] != "call_a" || estimate.ToolCallIDs[1] != "call_b" {
		t.Fatalf("unexpected matched calls: %+v", estimate.ToolCallIDs)
	}
}

func TestEstimateToolTimingRejectsUnmatchedAdjacentRequest(t *testing.T) {
	t.Parallel()
	_, store, _ := newTestServer(t, "http://127.0.0.1:1", "", 1024)
	project, err := store.GetProject(context.Background(), "prj_test")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-time.Second)
	startCapturedRequest(t, store, "req_other", project.CaptureSessionID, started, `{}`)
	finishCapturedRequest(t, store, "req_other", started.Add(100*time.Millisecond),
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_other"}]}}]}`)
	startCapturedRequest(t, store, "req_current", project.CaptureSessionID, started.Add(500*time.Millisecond),
		`{"messages":[{"role":"tool","tool_call_id":"call_expected","content":"done"}]}`)
	current, err := store.GetRequest(context.Background(), "req_current")
	if err != nil {
		t.Fatal(err)
	}
	estimate, err := store.EstimateToolTiming(context.Background(), current)
	if err != nil {
		t.Fatal(err)
	}
	if estimate != nil {
		t.Fatalf("unmatched request produced estimate: %+v", estimate)
	}
}

func startCapturedRequest(t *testing.T, store *Store, id, sessionID string, started time.Time, body string) {
	t.Helper()
	if err := store.StartRequest(context.Background(), StartRequestParams{
		ID: id, ProjectID: "prj_test", Method: http.MethodPost, Path: "/v1/chat/completions",
		UpstreamURL: "http://upstream.test/v1/chat/completions", StartedAt: started,
		RequestHeaders: jsonObject{}, CaptureSessionID: sessionID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRequestCapture(context.Background(), id, "test", false, []byte(body), int64(len(body)), false); err != nil {
		t.Fatal(err)
	}
}

func finishCapturedRequest(t *testing.T, store *Store, id string, finished time.Time, body string) {
	t.Helper()
	status := http.StatusOK
	if err := store.FinishRequest(context.Background(), FinishRequestParams{
		ID: id, Status: "completed", HTTPStatus: &status, FinishedAt: finished,
		DurationMS: 500, ResponseHeaders: jsonObject{}, ResponseBody: []byte(body), ResponseBytes: int64(len(body)),
	}); err != nil {
		t.Fatal(err)
	}
}

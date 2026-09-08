package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestProxyForwardsChatAndCapturesResponse(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" || request.URL.RawQuery != "trace=yes" {
			t.Errorf("unexpected upstream URL %s", request.URL.String())
		}
		if request.Header.Get("Authorization") != "Bearer upstream-secret" {
			t.Errorf("unexpected authorization %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("X-Debug-Trace") != "preserved" || request.Header.Get("Accept-Encoding") != "identity" {
			t.Errorf("headers were not forwarded correctly: %#v", request.Header)
		}
		body, _ := io.ReadAll(request.Body)
		if string(body) != `{"model":"vendor-model","messages":[{"role":"user","content":"hello"}]}` {
			t.Errorf("request body changed: %s", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Upstream-ID", "up_1")
		_, _ = writer.Write([]byte(`{"id":"chat_1","choices":[{"message":{"role":"assistant","content":"world"}}]}`))
	}))
	defer upstream.Close()

	server, store, key := newTestServer(t, upstream.URL+"/v1", "upstream-secret", 1<<20)
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()
	body := `{"model":"vendor-model","messages":[{"role":"user","content":"hello"}]}`
	request, _ := http.NewRequest(http.MethodPost, proxy.URL+"/v1/chat/completions?trace=yes", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Debug-Trace", "preserved")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	responseBody, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(responseBody, []byte("world")) {
		t.Fatalf("unexpected response %d %s", response.StatusCode, responseBody)
	}
	requestID := response.Header.Get("X-LLM-Proxy-Request-ID")
	log, err := store.GetRequest(context.Background(), requestID)
	if err != nil {
		t.Fatal(err)
	}
	if log.Model != "vendor-model" || log.Streaming || log.Status != "completed" || log.ResponseHeaders["X-Upstream-Id"][0] != "up_1" {
		t.Fatalf("unexpected capture: %+v", log)
	}
	if log.RequestHeaders["Authorization"][0] != "[REDACTED]" || log.RequestBody != body || string(responseBody) != log.ResponseBody {
		t.Fatalf("capture mismatch: %+v", log)
	}
}

func TestProxyCaptureLimitDoesNotLimitForwarding(t *testing.T) {
	t.Parallel()
	fullBody := strings.Repeat("x", 256)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = writer.Write([]byte(fullBody))
	}))
	defer upstream.Close()
	server, store, key := newTestServer(t, upstream.URL, "", 32)
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()
	request, _ := http.NewRequest(http.MethodGet, proxy.URL+"/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != fullBody {
		t.Fatalf("client received %d bytes, want %d", len(body), len(fullBody))
	}
	log, err := store.GetRequest(context.Background(), response.Header.Get("X-LLM-Proxy-Request-ID"))
	if err != nil {
		t.Fatal(err)
	}
	if !log.ResponseTruncated || len(log.ResponseBody) != 32 || log.ResponseBytes != 256 {
		t.Fatalf("unexpected capture limit result: %+v", log)
	}
}

func TestCaptureCanPauseSaveGroupAndStartANewRound(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer upstream.Close()
	server, store, key := newTestServer(t, upstream.URL, "", 1<<20)
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	callModels := func() string {
		request, _ := http.NewRequest(http.MethodGet, proxy.URL+"/v1/models", nil)
		request.Header.Set("Authorization", "Bearer "+key)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("models returned %d", response.StatusCode)
		}
		return response.Header.Get("X-LLM-Proxy-Request-ID")
	}
	postAPI := func(path, body string, target any) int {
		response, err := http.Post(proxy.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if target != nil {
			if err := json.NewDecoder(response.Body).Decode(target); err != nil {
				t.Fatal(err)
			}
		}
		return response.StatusCode
	}

	firstID := callModels()
	if _, err := store.GetRequest(context.Background(), firstID); err != nil {
		t.Fatalf("active capture did not save request: %v", err)
	}
	secondID := callModels()
	var paused Project
	if status := postAPI("/api/projects/prj_test/capture/pause", "", &paused); status != http.StatusOK {
		t.Fatalf("pause returned %d", status)
	}
	if paused.CaptureState != "paused" || paused.CaptureRequestCount != 2 {
		t.Fatalf("unexpected paused state: %+v", paused)
	}

	uncapturedID := callModels()
	if _, err := store.GetRequest(context.Background(), uncapturedID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("request made while paused was captured: %v", err)
	}
	if status := postAPI("/api/projects/prj_test/capture-groups", `{"name":"empty selection","requestIds":[]}`, nil); status != http.StatusBadRequest {
		t.Fatalf("save group without selected requests returned %d", status)
	}
	if status := postAPI("/api/projects/prj_test/capture-groups", `{"name":"missing selection","requestIds":["req_missing"]}`, nil); status != http.StatusBadRequest {
		t.Fatalf("save group with an unknown request returned %d", status)
	}
	var group CaptureGroup
	if status := postAPI("/api/projects/prj_test/capture-groups", `{"name":"first round","requestIds":["`+firstID+`"]}`, &group); status != http.StatusCreated {
		t.Fatalf("save group returned %d", status)
	}
	if group.Name != "first round" || group.RequestCount != 1 {
		t.Fatalf("unexpected group: %+v", group)
	}
	groupRequests, err := store.ListRequests(context.Background(), ListRequestsParams{ProjectID: "prj_test", GroupID: group.ID})
	if err != nil || len(groupRequests.Items) != 1 || groupRequests.Items[0].ID != firstID || groupRequests.Items[0].GroupID != group.ID {
		t.Fatalf("unexpected grouped requests: %+v, %v", groupRequests, err)
	}
	allRequests, err := store.ListRequests(context.Background(), ListRequestsParams{ProjectID: "prj_test"})
	if err != nil || len(allRequests.Items) != 2 || allRequests.Items[0].ID != secondID || allRequests.Items[0].GroupID != "" || allRequests.Items[1].ID != firstID || allRequests.Items[1].GroupID != group.ID {
		t.Fatalf("all requests did not include capture group: %+v, %v", allRequests, err)
	}

	var started Project
	if status := postAPI("/api/projects/prj_test/capture/start", "", &started); status != http.StatusOK {
		t.Fatalf("start returned %d", status)
	}
	if started.CaptureState != "capturing" || started.CaptureRequestCount != 0 {
		t.Fatalf("unexpected new capture state: %+v", started)
	}
	thirdID := callModels()
	if _, err := store.GetRequest(context.Background(), thirdID); err != nil {
		t.Fatalf("new capture round did not save request: %v", err)
	}
	groups, err := store.ListCaptureGroups(context.Background(), "prj_test")
	if err != nil || len(groups) != 1 || groups[0].RequestCount != 1 {
		t.Fatalf("saved group changed after new round: %+v, %v", groups, err)
	}
}

func TestProxyStreamsAndStoresAggregatedAssistantMessage(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		_, _ = writer.Write([]byte("data: {\"id\":\"chat_stream\",\"model\":\"stream-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello \"}}]}\n\n"))
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
		_, _ = writer.Write([]byte("data: {\"id\":\"chat_stream\",\"model\":\"stream-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"stream\"},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":4}}\n\ndata: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer upstream.Close()
	server, store, key := newTestServer(t, upstream.URL, "", 1<<20)
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()
	request, _ := http.NewRequest(http.MethodPost, proxy.URL+"/v1/chat/completions", strings.NewReader(`{"model":"stream-model","stream":true,"messages":[]}`))
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !bytes.Contains(body, []byte("[DONE]")) || !bytes.Contains(body, []byte("hello ")) {
		t.Fatalf("stream was not forwarded: %s", body)
	}
	log, err := store.GetRequest(context.Background(), response.Header.Get("X-LLM-Proxy-Request-ID"))
	if err != nil {
		t.Fatal(err)
	}
	var aggregate struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage map[string]int `json:"usage"`
	}
	if err := json.Unmarshal([]byte(log.AggregatedResponse), &aggregate); err != nil {
		t.Fatal(err)
	}
	if aggregate.Choices[0].Message.Content != "hello stream" || aggregate.Usage["total_tokens"] != 4 {
		t.Fatalf("unexpected aggregate %s", log.AggregatedResponse)
	}
}

func TestRotatingProjectKeyImmediatelyInvalidatesOldKey(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer upstream.Close()
	server, store, oldKey := newTestServer(t, upstream.URL, "", 1024)
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()
	callModels := func(key string) int {
		request, _ := http.NewRequest(http.MethodGet, proxy.URL+"/v1/models", nil)
		request.Header.Set("Authorization", "Bearer "+key)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	if status := callModels(oldKey); status != http.StatusOK {
		t.Fatalf("old key initially returned %d", status)
	}
	newKey, _ := GenerateProjectKey()
	encrypted, _ := server.vault.Encrypt(newKey)
	if err := store.RotateProjectKey(context.Background(), "prj_test", HashProjectKey(newKey), encrypted, projectKeyPrefix(newKey)); err != nil {
		t.Fatal(err)
	}
	if status := callModels(oldKey); status != http.StatusUnauthorized {
		t.Fatalf("old key after rotation returned %d", status)
	}
	if status := callModels(newKey); status != http.StatusOK {
		t.Fatalf("new key returned %d", status)
	}
}

func TestRetentionDeletesExpiredAndExcessFinishedRequests(t *testing.T) {
	t.Parallel()
	server, store, _ := newTestServer(t, "http://127.0.0.1:1", "", 1024)
	_ = server
	now := time.Now().UTC()
	for index, age := range []time.Duration{72 * time.Hour, 2 * time.Hour, time.Hour, 0} {
		id := fmt.Sprintf("retention_%d", index)
		started := now.Add(-age)
		if err := store.StartRequest(context.Background(), StartRequestParams{ID: id, ProjectID: "prj_test", Method: "GET", Path: "/v1/models", UpstreamURL: "http://upstream/models", StartedAt: started, RequestHeaders: jsonObject{}}); err != nil {
			t.Fatal(err)
		}
		status := http.StatusOK
		if err := store.FinishRequest(context.Background(), FinishRequestParams{ID: id, Status: "completed", HTTPStatus: &status, FinishedAt: started.Add(time.Second), DurationMS: 1000, ResponseHeaders: jsonObject{}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Cleanup(context.Background(), 2, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRequest(context.Background(), "retention_0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired request still exists: %v", err)
	}
	if _, err := store.GetRequest(context.Background(), "retention_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("excess request still exists: %v", err)
	}
	for _, id := range []string{"retention_2", "retention_3"} {
		if _, err := store.GetRequest(context.Background(), id); err != nil {
			t.Fatalf("kept request %s missing: %v", id, err)
		}
	}
}

func TestUnsupportedEndpointAndInvalidKeyUseOpenAIErrorShape(t *testing.T) {
	t.Parallel()
	server, _, _ := newTestServer(t, "http://127.0.0.1:1/v1", "", 1024)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil))
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil || recorder.Code != http.StatusNotFound || payload.Error.Code != "unsupported_endpoint" {
		t.Fatalf("unexpected unsupported response %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected invalid key response %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestCredentialsAreNotStoredInPlaintext(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "llm-proxy.db")
	vault, err := LoadOrCreateVault(dir, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	upstreamSecret := "sk-plaintext-must-not-appear"
	projectKey := "llmp_plaintext-must-not-appear"
	upstreamEncrypted, _ := vault.Encrypt(upstreamSecret)
	projectEncrypted, _ := vault.Encrypt(projectKey)
	_, err = store.CreateProject(context.Background(), CreateProjectParams{ID: "p", Name: "secure", BaseURL: "http://example.test/v1", UpstreamKeyEncrypted: upstreamEncrypted, APIKeyHash: HashProjectKey(projectKey), APIKeyEncrypted: projectEncrypted, APIKeyPrefix: projectKeyPrefix(projectKey)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	files := []string{databasePath, databasePath + "-wal"}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(upstreamSecret)) || bytes.Contains(data, []byte(projectKey)) {
			t.Fatalf("plaintext secret found in %s", file)
		}
	}
}

func newTestServer(t *testing.T, baseURL, upstreamKey string, captureLimit int64) (*Server, *Store, string) {
	t.Helper()
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "llm-proxy.db")
	vault, err := LoadOrCreateVault(dir, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	projectKey, _ := GenerateProjectKey()
	upstreamEncrypted, _ := vault.Encrypt(upstreamKey)
	projectEncrypted, _ := vault.Encrypt(projectKey)
	_, err = store.CreateProject(context.Background(), CreateProjectParams{ID: "prj_test", Name: "test", BaseURL: baseURL, UpstreamKeyEncrypted: upstreamEncrypted, APIKeyHash: HashProjectKey(projectKey), APIKeyEncrypted: projectEncrypted, APIKeyPrefix: projectKeyPrefix(projectKey)})
	if err != nil {
		t.Fatal(err)
	}
	static := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok"), Mode: fs.FileMode(0o644)}}
	server := NewServer(Config{CaptureMaxBytes: captureLimit, RetentionDays: 7, MaxRequestsPerProject: 10000, UpstreamHeaderTimeout: 2 * time.Second}, store, vault, static, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return server, store, projectKey
}

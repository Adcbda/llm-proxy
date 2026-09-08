package app

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenStoreMigratesDatabaseWithoutCaptureSessions(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE projects (
			id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, base_url TEXT NOT NULL,
			upstream_key_enc TEXT NOT NULL DEFAULT '', api_key_hash TEXT NOT NULL UNIQUE,
			api_key_enc TEXT NOT NULL, api_key_prefix TEXT NOT NULL,
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE request_logs (
			id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			method TEXT NOT NULL, path TEXT NOT NULL, upstream_url TEXT NOT NULL,
			model TEXT NOT NULL DEFAULT '', streaming INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL, http_status INTEGER, error TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL, finished_at TEXT, duration_ms INTEGER NOT NULL DEFAULT 0,
			request_headers TEXT NOT NULL DEFAULT '{}', response_headers TEXT NOT NULL DEFAULT '{}',
			request_body BLOB NOT NULL DEFAULT X'', response_body BLOB NOT NULL DEFAULT X'',
			aggregated_response TEXT NOT NULL DEFAULT '', request_truncated INTEGER NOT NULL DEFAULT 0,
			response_truncated INTEGER NOT NULL DEFAULT 0, request_bytes INTEGER NOT NULL DEFAULT 0,
			response_bytes INTEGER NOT NULL DEFAULT 0
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	now := formatTime(time.Now().UTC())
	if _, err := db.Exec(`INSERT INTO projects
		(id, name, base_url, upstream_key_enc, api_key_hash, api_key_enc, api_key_prefix, created_at, updated_at)
		VALUES ('legacy', 'legacy', 'http://example.test', '', 'hash', 'encrypted', 'prefix', ?, ?)`, now, now); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.GetProject(context.Background(), "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if project.CaptureState != "capturing" || project.CaptureSessionID == "" || project.CaptureStartedAt == nil {
		t.Fatalf("legacy project was not given an active capture session: %+v", project)
	}
	startedAt := time.Now().UTC()
	if err := store.StartRequest(context.Background(), StartRequestParams{
		ID: "req_legacy", ProjectID: project.ID, Method: "GET", Path: "/v1/models",
		UpstreamURL: "http://example.test/v1/models", StartedAt: startedAt,
		CaptureSessionID: project.CaptureSessionID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRequest(context.Background(), FinishRequestParams{
		ID: "req_legacy", Status: "completed", FinishedAt: startedAt.Add(time.Millisecond),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PauseCapture(context.Background(), project.ID); err != nil {
		t.Fatal(err)
	}
	group, err := store.SaveCaptureGroup(context.Background(), project.ID, "grp_legacy", "legacy round", []string{"req_legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if group.Name != "legacy round" || group.RequestCount != 1 {
		t.Fatalf("unexpected migrated capture group: %+v", group)
	}
}

func TestOpenStoreBackfillsLegacyCaptureGroupMembership(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "legacy-groups.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(context.Background(), CreateProjectParams{
		ID: "prj_legacy_group", Name: "legacy group", BaseURL: "http://example.test",
		APIKeyHash: "legacy-group-hash", APIKeyEncrypted: "encrypted", APIKeyPrefix: "prefix",
	})
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	if err := store.StartRequest(context.Background(), StartRequestParams{
		ID: "req_legacy_group", ProjectID: project.ID, Method: "GET", Path: "/v1/models",
		UpstreamURL: "http://example.test/v1/models", StartedAt: startedAt,
		CaptureSessionID: project.CaptureSessionID,
	}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.FinishRequest(context.Background(), FinishRequestParams{
		ID: "req_legacy_group", Status: "completed", FinishedAt: startedAt.Add(time.Millisecond),
	}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO capture_groups
		(id, project_id, session_id, name, started_at, ended_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, "grp_legacy_session", project.ID, project.CaptureSessionID,
		"legacy session group", formatTime(startedAt), formatTime(startedAt.Add(time.Millisecond)),
		formatTime(startedAt.Add(time.Second))); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requests, err := store.ListRequests(context.Background(), ListRequestsParams{ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(requests.Items) != 1 || requests.Items[0].GroupID != "grp_legacy_session" {
		t.Fatalf("legacy group membership was not backfilled: %+v", requests.Items)
	}
	groups, err := store.ListCaptureGroups(context.Background(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].RequestCount != 1 {
		t.Fatalf("legacy group request count was not preserved: %+v", groups)
	}
}

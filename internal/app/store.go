package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			base_url TEXT NOT NULL,
			upstream_key_enc TEXT NOT NULL DEFAULT '',
			api_key_hash TEXT NOT NULL UNIQUE,
			api_key_enc TEXT NOT NULL,
			api_key_prefix TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS request_logs (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			method TEXT NOT NULL,
			path TEXT NOT NULL,
			upstream_url TEXT NOT NULL,
			model TEXT NOT NULL DEFAULT '',
			streaming INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			http_status INTEGER,
			error TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL,
			finished_at TEXT,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			request_headers TEXT NOT NULL DEFAULT '{}',
			response_headers TEXT NOT NULL DEFAULT '{}',
			request_body BLOB NOT NULL DEFAULT X'',
			response_body BLOB NOT NULL DEFAULT X'',
			aggregated_response TEXT NOT NULL DEFAULT '',
			request_truncated INTEGER NOT NULL DEFAULT 0,
			response_truncated INTEGER NOT NULL DEFAULT 0,
			request_bytes INTEGER NOT NULL DEFAULT 0,
			response_bytes INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_project_started
			ON request_logs(project_id, started_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_retention
			ON request_logs(project_id, status, started_at)`,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (1, datetime('now'))`,
		`PRAGMA optimize`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply database migration: %w", err)
		}
	}
	return nil
}

func (s *Store) MarkInterrupted(ctx context.Context) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `UPDATE request_logs
		SET status = 'interrupted', error = 'service restarted before request completed',
			finished_at = ?, duration_ms = CAST((julianday(?) - julianday(started_at)) * 86400000 AS INTEGER)
		WHERE status = 'running'`, formatTime(now), formatTime(now))
	return err
}

type CreateProjectParams struct {
	ID                   string
	Name                 string
	BaseURL              string
	UpstreamKeyEncrypted string
	APIKeyHash           string
	APIKeyEncrypted      string
	APIKeyPrefix         string
}

func (s *Store) CreateProject(ctx context.Context, params CreateProjectParams) (Project, error) {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO projects
		(id, name, base_url, upstream_key_enc, api_key_hash, api_key_enc, api_key_prefix, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, params.ID, params.Name, params.BaseURL,
		params.UpstreamKeyEncrypted, params.APIKeyHash, params.APIKeyEncrypted, params.APIKeyPrefix,
		formatTime(now), formatTime(now))
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, params.ID)
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, p.name, p.base_url, p.upstream_key_enc,
		p.api_key_enc, p.api_key_prefix, p.created_at, p.updated_at, COUNT(r.id)
		FROM projects p LEFT JOIN request_logs r ON r.project_id = p.id
		GROUP BY p.id ORDER BY p.created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := make([]Project, 0)
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func (s *Store) GetProject(ctx context.Context, id string) (Project, error) {
	row := s.db.QueryRowContext(ctx, `SELECT p.id, p.name, p.base_url, p.upstream_key_enc,
		p.api_key_enc, p.api_key_prefix, p.created_at, p.updated_at, COUNT(r.id)
		FROM projects p LEFT JOIN request_logs r ON r.project_id = p.id
		WHERE p.id = ? GROUP BY p.id`, id)
	project, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return project, err
}

func (s *Store) GetProjectByKeyHash(ctx context.Context, hash string) (Project, error) {
	row := s.db.QueryRowContext(ctx, `SELECT p.id, p.name, p.base_url, p.upstream_key_enc,
		p.api_key_enc, p.api_key_prefix, p.created_at, p.updated_at, COUNT(r.id)
		FROM projects p LEFT JOIN request_logs r ON r.project_id = p.id
		WHERE p.api_key_hash = ? GROUP BY p.id`, hash)
	project, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return project, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanProject(row scanner) (Project, error) {
	var project Project
	var created, updated string
	err := row.Scan(&project.ID, &project.Name, &project.BaseURL, &project.UpstreamKeyEncrypted,
		&project.ProjectKeyEncrypted, &project.APIKeyPrefix, &created, &updated, &project.RequestCount)
	if err != nil {
		return Project{}, err
	}
	project.CreatedAt, err = parseTime(created)
	if err != nil {
		return Project{}, err
	}
	project.UpdatedAt, err = parseTime(updated)
	return project, err
}

type UpdateProjectParams struct {
	Name                 *string
	BaseURL              *string
	UpstreamKeyEncrypted *string
}

func (s *Store) UpdateProject(ctx context.Context, id string, params UpdateProjectParams) (Project, error) {
	sets := []string{"updated_at = ?"}
	args := []any{formatTime(time.Now().UTC())}
	if params.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *params.Name)
	}
	if params.BaseURL != nil {
		sets = append(sets, "base_url = ?")
		args = append(args, *params.BaseURL)
	}
	if params.UpstreamKeyEncrypted != nil {
		sets = append(sets, "upstream_key_enc = ?")
		args = append(args, *params.UpstreamKeyEncrypted)
	}
	args = append(args, id)
	result, err := s.db.ExecContext(ctx, `UPDATE projects SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return Project{}, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return Project{}, ErrNotFound
	}
	return s.GetProject(ctx, id)
}

func (s *Store) RotateProjectKey(ctx context.Context, id, hash, encrypted, prefix string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE projects SET api_key_hash = ?, api_key_enc = ?,
		api_key_prefix = ?, updated_at = ? WHERE id = ?`, hash, encrypted, prefix,
		formatTime(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteProject(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

type StartRequestParams struct {
	ID             string
	ProjectID      string
	Method         string
	Path           string
	UpstreamURL    string
	RequestHeaders jsonObject
	StartedAt      time.Time
}

func (s *Store) StartRequest(ctx context.Context, params StartRequestParams) error {
	headers, _ := json.Marshal(params.RequestHeaders)
	_, err := s.db.ExecContext(ctx, `INSERT INTO request_logs
		(id, project_id, method, path, upstream_url, status, started_at, request_headers)
		VALUES (?, ?, ?, ?, ?, 'running', ?, ?)`, params.ID, params.ProjectID, params.Method,
		params.Path, params.UpstreamURL, formatTime(params.StartedAt), string(headers))
	return err
}

func (s *Store) UpdateRequestCapture(ctx context.Context, id, model string, streaming bool, body []byte, total int64, truncated bool) error {
	if body == nil {
		body = []byte{}
	}
	_, err := s.db.ExecContext(ctx, `UPDATE request_logs SET model = ?, streaming = ?, request_body = ?,
		request_bytes = ?, request_truncated = ? WHERE id = ?`, model, boolInt(streaming), body,
		total, boolInt(truncated), id)
	return err
}

type FinishRequestParams struct {
	ID                 string
	Status             string
	HTTPStatus         *int
	Error              string
	FinishedAt         time.Time
	DurationMS         int64
	ResponseHeaders    jsonObject
	ResponseBody       []byte
	AggregatedResponse string
	ResponseTruncated  bool
	ResponseBytes      int64
}

func (s *Store) FinishRequest(ctx context.Context, params FinishRequestParams) error {
	if params.ResponseBody == nil {
		params.ResponseBody = []byte{}
	}
	headers, _ := json.Marshal(params.ResponseHeaders)
	_, err := s.db.ExecContext(ctx, `UPDATE request_logs SET status = ?, http_status = ?, error = ?,
		finished_at = ?, duration_ms = ?, response_headers = ?, response_body = ?, aggregated_response = ?,
		response_truncated = ?, response_bytes = ? WHERE id = ?`, params.Status, params.HTTPStatus,
		params.Error, formatTime(params.FinishedAt), params.DurationMS, string(headers), params.ResponseBody,
		params.AggregatedResponse, boolInt(params.ResponseTruncated), params.ResponseBytes, params.ID)
	return err
}

type ListRequestsParams struct {
	ProjectID string
	Limit     int
	Cursor    string
	Status    string
	Path      string
	Model     string
	Streaming *bool
}

func (s *Store) ListRequests(ctx context.Context, params ListRequestsParams) (ListRequestsResult, error) {
	if params.Limit <= 0 || params.Limit > 100 {
		params.Limit = 50
	}
	where := []string{"project_id = ?"}
	args := []any{params.ProjectID}
	if params.Status != "" {
		where = append(where, "status = ?")
		args = append(args, params.Status)
	}
	if params.Path != "" {
		where = append(where, "path = ?")
		args = append(args, params.Path)
	}
	if params.Model != "" {
		where = append(where, "model LIKE ?")
		args = append(args, "%"+params.Model+"%")
	}
	if params.Streaming != nil {
		where = append(where, "streaming = ?")
		args = append(args, boolInt(*params.Streaming))
	}
	if params.Cursor != "" {
		startedAt, id, err := decodeCursor(params.Cursor)
		if err != nil {
			return ListRequestsResult{}, err
		}
		where = append(where, "(started_at < ? OR (started_at = ? AND id < ?))")
		args = append(args, startedAt, startedAt, id)
	}
	args = append(args, params.Limit+1)
	query := `SELECT id, project_id, method, path, model, streaming, status, http_status,
		started_at, finished_at, duration_ms, request_truncated, response_truncated,
		request_bytes, response_bytes FROM request_logs WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY started_at DESC, id DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return ListRequestsResult{}, err
	}
	defer rows.Close()
	items := make([]RequestSummary, 0, params.Limit)
	for rows.Next() {
		var item RequestSummary
		var stream, reqTrunc, respTrunc int
		var started string
		var finished sql.NullString
		var httpStatus sql.NullInt64
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.Method, &item.Path, &item.Model,
			&stream, &item.Status, &httpStatus, &started, &finished, &item.DurationMS,
			&reqTrunc, &respTrunc, &item.RequestBytes, &item.ResponseBytes); err != nil {
			return ListRequestsResult{}, err
		}
		item.Streaming = stream != 0
		item.RequestTruncated, item.ResponseTruncated = reqTrunc != 0, respTrunc != 0
		item.StartedAt, err = parseTime(started)
		if err != nil {
			return ListRequestsResult{}, err
		}
		if finished.Valid {
			t, parseErr := parseTime(finished.String)
			if parseErr != nil {
				return ListRequestsResult{}, parseErr
			}
			item.FinishedAt = &t
		}
		if httpStatus.Valid {
			value := int(httpStatus.Int64)
			item.HTTPStatus = &value
		}
		items = append(items, item)
	}
	result := ListRequestsResult{Items: items}
	if len(items) > params.Limit {
		last := items[params.Limit-1]
		result.Items = items[:params.Limit]
		result.NextCursor = encodeCursor(last.StartedAt, last.ID)
	}
	return result, rows.Err()
}

func (s *Store) GetRequest(ctx context.Context, id string) (RequestLog, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, project_id, method, path, upstream_url, model,
		streaming, status, http_status, error, started_at, finished_at, duration_ms,
		request_headers, response_headers, request_body, response_body, aggregated_response,
		request_truncated, response_truncated, request_bytes, response_bytes
		FROM request_logs WHERE id = ?`, id)
	var log RequestLog
	var stream, reqTrunc, respTrunc int
	var started string
	var finished sql.NullString
	var httpStatus sql.NullInt64
	var reqHeaders, respHeaders string
	var reqBody, respBody []byte
	err := row.Scan(&log.ID, &log.ProjectID, &log.Method, &log.Path, &log.UpstreamURL, &log.Model,
		&stream, &log.Status, &httpStatus, &log.Error, &started, &finished, &log.DurationMS,
		&reqHeaders, &respHeaders, &reqBody, &respBody, &log.AggregatedResponse,
		&reqTrunc, &respTrunc, &log.RequestBytes, &log.ResponseBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return RequestLog{}, ErrNotFound
	}
	if err != nil {
		return RequestLog{}, err
	}
	log.Streaming = stream != 0
	log.RequestTruncated, log.ResponseTruncated = reqTrunc != 0, respTrunc != 0
	log.RequestBody, log.ResponseBody = string(reqBody), string(respBody)
	log.StartedAt, err = parseTime(started)
	if err != nil {
		return RequestLog{}, err
	}
	if finished.Valid {
		t, parseErr := parseTime(finished.String)
		if parseErr != nil {
			return RequestLog{}, parseErr
		}
		log.FinishedAt = &t
	}
	if httpStatus.Valid {
		value := int(httpStatus.Int64)
		log.HTTPStatus = &value
	}
	log.RequestHeaders, log.ResponseHeaders = jsonObject{}, jsonObject{}
	_ = json.Unmarshal([]byte(reqHeaders), &log.RequestHeaders)
	_ = json.Unmarshal([]byte(respHeaders), &log.ResponseHeaders)
	return log, nil
}

func (s *Store) ClearProjectRequests(ctx context.Context, projectID string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM request_logs WHERE project_id = ? AND status != 'running'`, projectID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) Cleanup(ctx context.Context, retentionDays, maxPerProject int) error {
	if retentionDays > 0 {
		cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
		if _, err := s.db.ExecContext(ctx, `DELETE FROM request_logs WHERE status != 'running' AND started_at < ?`, formatTime(cutoff)); err != nil {
			return err
		}
	}
	if maxPerProject > 0 {
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM projects`)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			_, err := s.db.ExecContext(ctx, `DELETE FROM request_logs WHERE id IN (
				SELECT id FROM request_logs WHERE project_id = ? AND status != 'running'
				ORDER BY started_at DESC, id DESC LIMIT -1 OFFSET ?
			)`, id, maxPerProject)
			if err != nil {
				return err
			}
		}
	}
	_, err := s.db.ExecContext(ctx, `PRAGMA optimize`)
	return err
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(value string) (time.Time, error) { return time.Parse(time.RFC3339Nano, value) }

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func encodeCursor(t time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(formatTime(t) + "|" + id))
}

func decodeCursor(cursor string) (string, string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", fmt.Errorf("invalid cursor")
	}
	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid cursor")
	}
	if _, err := time.Parse(time.RFC3339Nano, parts[0]); err != nil {
		return "", "", fmt.Errorf("invalid cursor time: %w", err)
	}
	if parts[1] == "" {
		return "", "", fmt.Errorf("invalid cursor id")
	}
	return parts[0], parts[1], nil
}

func projectKeyPrefix(key string) string {
	if len(key) <= 12 {
		return key
	}
	return key[:12]
}

func parseOptionalBool(value string) (*bool, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return nil, fmt.Errorf("invalid boolean value %q", value)
	}
	return &parsed, nil
}

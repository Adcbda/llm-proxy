package app

import "time"

type Project struct {
	ID                   string     `json:"id"`
	Name                 string     `json:"name"`
	BaseURL              string     `json:"baseUrl"`
	UpstreamAPIKeyMasked string     `json:"upstreamApiKeyMasked"`
	APIKeyPrefix         string     `json:"apiKeyPrefix"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	RequestCount         int        `json:"requestCount"`
	CaptureState         string     `json:"captureState"`
	CaptureStartedAt     *time.Time `json:"captureStartedAt"`
	CapturePausedAt      *time.Time `json:"capturePausedAt"`
	CaptureRequestCount  int        `json:"captureRequestCount"`
	UpstreamKeyEncrypted string     `json:"-"`
	ProjectKeyEncrypted  string     `json:"-"`
	CaptureSessionID     string     `json:"-"`
}

type CaptureGroup struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"projectId"`
	Name         string    `json:"name"`
	StartedAt    time.Time `json:"startedAt"`
	EndedAt      time.Time `json:"endedAt"`
	CreatedAt    time.Time `json:"createdAt"`
	RequestCount int       `json:"requestCount"`
}

type RequestLog struct {
	ID                 string     `json:"id"`
	ProjectID          string     `json:"projectId"`
	Method             string     `json:"method"`
	Path               string     `json:"path"`
	UpstreamURL        string     `json:"upstreamUrl"`
	Model              string     `json:"model"`
	Streaming          bool       `json:"streaming"`
	Status             string     `json:"status"`
	HTTPStatus         *int       `json:"httpStatus"`
	Error              string     `json:"error,omitempty"`
	StartedAt          time.Time  `json:"startedAt"`
	FinishedAt         *time.Time `json:"finishedAt"`
	DurationMS         int64      `json:"durationMs"`
	RequestHeaders     jsonObject `json:"requestHeaders"`
	ResponseHeaders    jsonObject `json:"responseHeaders"`
	RequestBody        string     `json:"requestBody"`
	ResponseBody       string     `json:"responseBody"`
	AggregatedResponse string     `json:"aggregatedResponse,omitempty"`
	RequestTruncated   bool       `json:"requestTruncated"`
	ResponseTruncated  bool       `json:"responseTruncated"`
	RequestBytes       int64      `json:"requestBytes"`
	ResponseBytes      int64      `json:"responseBytes"`
	Live               bool       `json:"live"`
}

type RequestSummary struct {
	ID                string     `json:"id"`
	ProjectID         string     `json:"projectId"`
	GroupID           string     `json:"groupId,omitempty"`
	Method            string     `json:"method"`
	Path              string     `json:"path"`
	Model             string     `json:"model"`
	Streaming         bool       `json:"streaming"`
	Status            string     `json:"status"`
	HTTPStatus        *int       `json:"httpStatus"`
	StartedAt         time.Time  `json:"startedAt"`
	FinishedAt        *time.Time `json:"finishedAt"`
	DurationMS        int64      `json:"durationMs"`
	RequestTruncated  bool       `json:"requestTruncated"`
	ResponseTruncated bool       `json:"responseTruncated"`
	RequestBytes      int64      `json:"requestBytes"`
	ResponseBytes     int64      `json:"responseBytes"`
}

type jsonObject map[string][]string

type ListRequestsResult struct {
	Items      []RequestSummary `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

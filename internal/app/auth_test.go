package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestManagementAuthentication(t *testing.T) {
	server := newAuthTestServer(Config{Username: "admin", Password: "admin"})

	statusResponse := performAuthRequest(server, http.MethodGet, "/api/auth/status", "", nil)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("status code = %d", statusResponse.Code)
	}
	var initialStatus authStatus
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &initialStatus); err != nil {
		t.Fatal(err)
	}
	if initialStatus.Authenticated || !initialStatus.AuthRequired {
		t.Fatalf("unexpected initial status: %+v", initialStatus)
	}

	protectedResponse := performAuthRequest(server, http.MethodGet, "/api/not-found", "", nil)
	if protectedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("protected API status = %d, want %d", protectedResponse.Code, http.StatusUnauthorized)
	}

	invalidResponse := performAuthRequest(server, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"wrong"}`, nil)
	if invalidResponse.Code != http.StatusUnauthorized {
		t.Fatalf("invalid login status = %d, want %d", invalidResponse.Code, http.StatusUnauthorized)
	}

	loginResponse := performAuthRequest(server, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"admin"}`, nil)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginResponse.Code, loginResponse.Body.String())
	}
	cookies := loginResponse.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %d, want 1", len(cookies))
	}
	session := cookies[0]
	if session.Name != sessionCookieName || !session.HttpOnly || session.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unexpected session cookie: %+v", session)
	}

	authenticatedStatusResponse := performAuthRequest(server, http.MethodGet, "/api/auth/status", "", session)
	var authenticatedStatus authStatus
	if err := json.Unmarshal(authenticatedStatusResponse.Body.Bytes(), &authenticatedStatus); err != nil {
		t.Fatal(err)
	}
	if !authenticatedStatus.Authenticated || authenticatedStatus.Username != "admin" {
		t.Fatalf("unexpected authenticated status: %+v", authenticatedStatus)
	}

	authenticatedAPIResponse := performAuthRequest(server, http.MethodGet, "/api/not-found", "", session)
	if authenticatedAPIResponse.Code != http.StatusNotFound {
		t.Fatalf("authenticated API status = %d, want %d", authenticatedAPIResponse.Code, http.StatusNotFound)
	}

	logoutResponse := performAuthRequest(server, http.MethodPost, "/api/auth/logout", "", session)
	if logoutResponse.Code != http.StatusOK {
		t.Fatalf("logout status = %d", logoutResponse.Code)
	}
	logoutCookies := logoutResponse.Result().Cookies()
	if len(logoutCookies) != 1 || logoutCookies[0].MaxAge >= 0 {
		t.Fatalf("logout did not clear session cookie: %+v", logoutCookies)
	}
}

func TestNoAuthBypassesManagementLogin(t *testing.T) {
	server := newAuthTestServer(Config{Username: "admin", Password: "admin", NoAuth: true})

	statusResponse := performAuthRequest(server, http.MethodGet, "/api/auth/status", "", nil)
	var status authStatus
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Authenticated || status.AuthRequired {
		t.Fatalf("unexpected noauth status: %+v", status)
	}

	apiResponse := performAuthRequest(server, http.MethodGet, "/api/not-found", "", nil)
	if apiResponse.Code != http.StatusNotFound {
		t.Fatalf("noauth API status = %d, want %d", apiResponse.Code, http.StatusNotFound)
	}
}

func TestProxyAndHealthRoutesDoNotRequireManagementLogin(t *testing.T) {
	server := newAuthTestServer(Config{Username: "admin", Password: "admin"})

	healthResponse := performAuthRequest(server, http.MethodGet, "/healthz", "", nil)
	if healthResponse.Code != http.StatusOK {
		t.Fatalf("health status = %d", healthResponse.Code)
	}
	proxyResponse := performAuthRequest(server, http.MethodGet, "/v1/unsupported", "", nil)
	if proxyResponse.Code != http.StatusNotFound {
		t.Fatalf("proxy status = %d, want %d", proxyResponse.Code, http.StatusNotFound)
	}
}

func newAuthTestServer(config Config) *Server {
	static := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	return NewServer(config, nil, nil, static, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func performAuthRequest(server *Server, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
)

const sessionCookieName = "llm_proxy_session"

type loginInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authStatus struct {
	Authenticated bool   `json:"authenticated"`
	AuthRequired  bool   `json:"authRequired"`
	Username      string `json:"username,omitempty"`
}

func newSessionToken() string {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		panic("generate session token: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(value)
}

func (server *Server) authRequired() bool {
	return !server.config.NoAuth && (server.config.Username != "" || server.config.Password != "")
}

func (server *Server) authenticated(request *http.Request) bool {
	if !server.authRequired() {
		return true
	}
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return secureEqual(cookie.Value, server.sessionToken)
}

func secureEqual(left, right string) bool {
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}

func (server *Server) routeAuth(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Add("Vary", "Cookie")
	switch request.URL.Path {
	case "/api/auth/status":
		if request.Method != http.MethodGet {
			server.methodNotAllowed(writer, http.MethodGet)
			return
		}
		server.writeAuthStatus(writer, request)
	case "/api/auth/login":
		if request.Method != http.MethodPost {
			server.methodNotAllowed(writer, http.MethodPost)
			return
		}
		server.login(writer, request)
	case "/api/auth/logout":
		if request.Method != http.MethodPost {
			server.methodNotAllowed(writer, http.MethodPost)
			return
		}
		server.logout(writer, request)
	default:
		writeAPIError(writer, http.StatusNotFound, "not found")
	}
}

func (server *Server) writeAuthStatus(writer http.ResponseWriter, request *http.Request) {
	status := authStatus{
		Authenticated: server.authenticated(request),
		AuthRequired:  server.authRequired(),
	}
	if status.Authenticated && status.AuthRequired {
		status.Username = server.config.Username
	}
	writeJSON(writer, http.StatusOK, status)
}

func (server *Server) login(writer http.ResponseWriter, request *http.Request) {
	if !server.authRequired() {
		server.writeAuthStatus(writer, request)
		return
	}
	var input loginInput
	if !decodeJSON(writer, request, &input) {
		return
	}
	usernameMatches := secureEqual(input.Username, server.config.Username)
	passwordMatches := secureEqual(input.Password, server.config.Password)
	if !usernameMatches || !passwordMatches {
		writeAPIError(writer, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    server.sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   request.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(writer, http.StatusOK, authStatus{Authenticated: true, AuthRequired: true, Username: server.config.Username})
}

func (server *Server) logout(writer http.ResponseWriter, request *http.Request) {
	http.SetCookie(writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   request.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(writer, http.StatusOK, authStatus{Authenticated: !server.authRequired(), AuthRequired: server.authRequired()})
}

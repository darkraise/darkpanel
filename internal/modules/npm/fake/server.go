// Package fake is an in-process stand-in for the Nginx Proxy Manager API: the status, user, token, proxy host, access
// list and certificate endpoints the npm module calls, with NPM's error body and the refusals of the pinned release.
package fake

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

// CreatedOnLayout is the format of created_on, modified_on and expires_on, always in UTC.
const CreatedOnLayout = "2006-01-02T15:04:05.000Z"

const tokenLifetime = 24 * time.Hour

// Request is one recorded request.
type Request = cloudfake.Request

// Fault is one injected failure for the next request that matches a method and a path. Status answers with that
// status without running the handler. AfterCommit runs the handler, so the write commits, and then closes the
// connection without an answer; it wins when both are set.
type Fault struct {
	Status      int
	AfterCommit bool
}

// Server is the fake NPM API. It is safe for concurrent use.
type Server struct {
	tb    testing.TB
	cloud *cloudfake.Server

	mu       sync.Mutex
	now      func() time.Time
	admin    *adminUser
	tokens   map[string]bool
	tokenSeq int

	hosts   []npmapi.ProxyHost
	hostSeq int
	lists   []npmapi.AccessList
	listSeq int
	certs   []npmapi.Certificate
	certSeq int
}

type adminUser struct{ email, password string }

// handler computes the answer of one request; it takes the server's lock itself and returns only copies.
type handler func(r *http.Request, body []byte) (status int, answer any)

// New starts a fake with no user and no objects, closed by tb.Cleanup.
func New(tb testing.TB) *Server {
	tb.Helper()
	s := &Server{tb: tb, cloud: cloudfake.New(tb), now: time.Now, tokens: map[string]bool{}}
	s.route(http.MethodGet, "/api/{$}", false, s.status)
	s.route(http.MethodPost, "/api/users", false, s.createUser)
	s.route(http.MethodPost, "/api/tokens", false, s.createToken)
	s.registerObjects()
	return s
}

// URL is the base URL of the fake, without a trailing slash.
func (s *Server) URL() string { return s.cloud.URL() }

// SetClock sets the source of created_on, modified_on and expires_on; the default is time.Now.
func (s *Server) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// Fail queues f for the next request that matches method and the exact URL path, oldest first; one request uses one
// fault. A fault with neither Status nor AfterCommit fails the test.
func (s *Server) Fail(method, path string, f Fault) {
	s.tb.Helper()
	switch {
	case f.AfterCommit:
		s.cloud.Fail(method, path, cloudfake.Fault{LoseResponse: true})
	case f.Status != 0:
		body, _ := json.Marshal(errorBody(f.Status, http.StatusText(f.Status)))
		s.cloud.Fail(method, path, cloudfake.Fault{Status: f.Status, Body: string(body)})
	default:
		s.tb.Fatalf("fake: a fault needs a Status or AfterCommit")
	}
}

// Requests returns every request received, in arrival order, faulted and refused ones included.
func (s *Server) Requests() []Request { return s.cloud.Requests() }

// Setup reports whether a user exists, as the status endpoint does.
func (s *Server) Setup() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.admin != nil
}

// Admin returns the stored administrator.
func (s *Server) Admin() (email, password string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.admin == nil {
		return "", "", false
	}
	return s.admin.email, s.admin.password, true
}

// SeedAdmin stores the administrator, replacing any, and sets setup to true.
func (s *Server) SeedAdmin(email, password string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.admin = &adminUser{email: email, password: password}
}

// stamp formats t in CreatedOnLayout, in UTC.
func (s *Server) stamp(t time.Time) string { return t.UTC().Format(CreatedOnLayout) }

// route registers h at path; an authenticated route answers 403 unless the request holds a token the fake issued.
func (s *Server) route(method, path string, authenticated bool, h handler) {
	s.cloud.Handle(method, path, func(w http.ResponseWriter, r *http.Request) {
		if authenticated && !s.authorized(r) {
			writeJSON(w, http.StatusForbidden, errorBody(http.StatusForbidden, "Permission Denied"))
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody(http.StatusBadRequest, "Bad Request"))
			return
		}
		status, answer := h(r, body)
		writeJSON(w, status, answer)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[token]
}

func (s *Server) status(*http.Request, []byte) (int, any) {
	return http.StatusOK, map[string]any{
		"status":  "OK",
		"setup":   s.Setup(),
		"version": map[string]int{"major": 2, "minor": 13, "revision": 5},
	}
}

func (s *Server) createUser(_ *http.Request, body []byte) (int, any) {
	var in struct {
		Name       string   `json:"name"`
		Nickname   string   `json:"nickname"`
		Email      string   `json:"email"`
		Roles      []string `json:"roles"`
		IsDisabled bool     `json:"is_disabled"`
		Auth       struct {
			Secret string `json:"secret"`
		} `json:"auth"`
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.admin != nil {
		return refusal(http.StatusForbidden, "Permission Denied")
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return refusal(http.StatusBadRequest, "Bad Request")
	}
	if in.Email == "" {
		return refusal(http.StatusBadRequest, "data/email must NOT have fewer than 3 characters")
	}
	if !slices.Contains(in.Roles, "admin") {
		return refusal(http.StatusBadRequest, "data/roles must contain the admin role")
	}
	s.admin = &adminUser{email: in.Email, password: in.Auth.Secret}
	return http.StatusCreated, map[string]any{
		"id": 1, "name": in.Name, "nickname": in.Nickname, "email": in.Email, "roles": in.Roles, "is_disabled": in.IsDisabled,
	}
}

func (s *Server) createToken(_ *http.Request, body []byte) (int, any) {
	var in struct {
		Identity string `json:"identity"`
		Secret   string `json:"secret"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return refusal(http.StatusBadRequest, "Bad Request")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.admin == nil || strings.ToLower(strings.TrimSpace(in.Identity)) != s.admin.email || in.Secret != s.admin.password {
		return refusal(http.StatusBadRequest, "Invalid email or password")
	}
	s.tokenSeq++
	token := fmt.Sprintf("fake-token-%d", s.tokenSeq)
	s.tokens[token] = true
	return http.StatusOK, map[string]string{"token": token, "expires": s.stamp(s.now().Add(tokenLifetime))}
}

func errorBody(code int, message string) map[string]any {
	return map[string]any{"error": map[string]any{"code": code, "message": message}}
}

func refusal(code int, message string) (int, any) { return code, errorBody(code, message) }

func notFound(id string) (int, any) { return refusal(http.StatusNotFound, "Not Found - "+id) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

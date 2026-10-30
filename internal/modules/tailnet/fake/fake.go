// Package fake is an in-process stand-in for the Tailscale API: the OAuth token endpoint and the device, route,
// auth key and policy endpoints the tailnet module calls, with the response shapes of the provider's documentation.
package fake

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
)

const (
	// ClientID is the OAuth client id the fake accepts.
	ClientID = "fake-client"
	// ClientSecret is the OAuth client secret the fake accepts.
	ClientSecret = "fake-secret"
	// Tailnet is the tailnet name the fake serves.
	Tailnet = "example.com"
)

const (
	messageUnauthorized = "API token invalid"
	messageForbidden    = "calling actor does not have enough permissions to perform this function"
	tokenLifetimeSecs   = 3600
)

// Server is the fake Tailscale API. It is safe for concurrent use.
type Server struct {
	cloud *cloudfake.Server

	mu      sync.Mutex
	scopes  []string
	tokens  map[string]struct{}
	seq     int
	devices []Device
	keys    []Key
	policy  string
	etag    string
}

// New starts a fake that grants every scope the module asks for, holds no devices or keys and the default
// policy, and is closed by tb.Cleanup.
func New(tb testing.TB) *Server {
	tb.Helper()
	s := &Server{
		cloud:  cloudfake.New(tb),
		scopes: []string{"devices:core", "devices:routes", "auth_keys", "policy_file", "devices:posture_attributes"},
		tokens: map[string]struct{}{},
	}
	s.policy = defaultPolicy
	s.etag = etagOf(defaultPolicy)
	s.cloud.Handle(http.MethodPost, "/api/v2/oauth/token", s.token)
	s.registerDevices()
	s.registerKeys()
	s.registerPolicy()
	return s
}

// URL is the base URL of the fake, without a trailing slash.
func (s *Server) URL() string { return s.cloud.URL() }

// Client is an HTTP client for the fake.
func (s *Server) Client() *http.Client { return s.cloud.Client() }

// Cloud is the shared scaffolding underneath: the request log and the fault queue.
func (s *Server) Cloud() *cloudfake.Server { return s.cloud }

// SetScopes replaces the scopes the fake grants; requests that need others answer 403.
func (s *Server) SetScopes(scopes ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scopes = slices.Clone(scopes)
}

// Tokens is the number of access tokens issued so far.
func (s *Server) Tokens() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tokens)
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if r.PostForm.Get("grant_type") != "client_credentials" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	if r.PostForm.Get("client_id") != ClientID || r.PostForm.Get("client_secret") != ClientSecret {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	token := "tskey-api-fake-" + strconv.Itoa(len(s.tokens)+1)
	s.tokens[token] = struct{}{}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   tokenLifetimeSecs,
		"scope":        strings.Join(s.scopes, " "),
	})
}

// route registers h behind the guard: a bearer token the fake issued, then one of the scopes in need, then a
// known tailnet when the pattern names one.
func (s *Server) route(method, pattern string, need []string, h http.HandlerFunc) {
	s.cloud.Handle(method, pattern, func(w http.ResponseWriter, r *http.Request) {
		if refusal := s.guard(r, need); refusal != 0 {
			switch refusal {
			case http.StatusUnauthorized:
				writeMessage(w, refusal, messageUnauthorized)
			case http.StatusForbidden:
				writeMessage(w, refusal, messageForbidden)
			default:
				writeMessage(w, refusal, "tailnet not found")
			}
			return
		}
		h(w, r)
	})
}

// guard answers the status that refuses r, or 0 when r may proceed.
func (s *Server) guard(r *http.Request, need []string) int {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, issued := s.tokens[token]; !ok || !issued {
		return http.StatusUnauthorized
	}
	if !slices.ContainsFunc(need, func(n string) bool { return slices.Contains(s.scopes, n) }) {
		return http.StatusForbidden
	}
	if t := r.PathValue("t"); t != "" && t != Tailnet && t != "-" {
		return http.StatusNotFound
	}
	return 0
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeMessage(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"message": message})
}

// nonNil returns a copy of s that is never nil, so it renders as [] in JSON.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return slices.Clone(s)
}

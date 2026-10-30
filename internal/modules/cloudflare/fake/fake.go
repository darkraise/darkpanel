// Package fake is an in-process stand-in for the Cloudflare API: the token verification, tunnel, tunnel
// configuration, zone and DNS record endpoints the cloudflare module calls, with Cloudflare's answer envelope.
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

	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
)

const (
	// Token is the API token the fake accepts.
	Token = "fake-token"
	// AccountID is the only account the fake serves.
	AccountID = "0123456789abcdef0123456789abcdef"
)

const (
	// PermTunnelWrite is the permission every tunnel endpoint needs.
	PermTunnelWrite = "Cloudflare Tunnel Write"
	// PermZoneRead is the permission the zone list needs.
	PermZoneRead = "Zone Read"
	// PermDNSWrite is the permission every DNS record endpoint needs.
	PermDNSWrite = "DNS Write"
)

const (
	apiPrefix     = "/client/v4"
	perPageMin    = 100
	codeInvalid   = 1000
	codeBadInput  = 1001
	codeForbidden = 10000
)

// Server is the fake Cloudflare API. It is safe for concurrent use.
type Server struct {
	cloud *cloudfake.Server

	mu      sync.Mutex
	perms   []string
	seq     int
	zones   []zone
	tunnels []*tunnelState
	records map[string][]*recordState // by zone id
}

type zone struct{ id, name string }

// New starts a fake that holds all three permissions, no zones, tunnels or records, and is closed by tb.Cleanup.
func New(tb testing.TB) *Server {
	tb.Helper()
	s := &Server{
		cloud:   cloudfake.New(tb),
		perms:   []string{PermTunnelWrite, PermZoneRead, PermDNSWrite},
		records: map[string][]*recordState{},
	}
	s.route(http.MethodGet, "/accounts/{a}/tokens/verify", "", s.verifyToken)
	s.route(http.MethodGet, "/user/tokens/verify", "", s.verifyToken)
	s.route(http.MethodGet, "/zones", PermZoneRead, s.listZones)
	s.registerTunnels()
	s.registerRecords()
	return s
}

// URL is the base URL of the fake, without a trailing slash; the API answers there with and without the
// "/client/v4" prefix.
func (s *Server) URL() string { return s.cloud.URL() }

// Client is an HTTP client for the fake.
func (s *Server) Client() *http.Client { return s.cloud.Client() }

// Cloud is the shared scaffolding underneath: the request log and the fault queue.
func (s *Server) Cloud() *cloudfake.Server { return s.cloud }

// SetPermissions replaces the permissions the token holds; requests that need others answer 403.
func (s *Server) SetPermissions(perms ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.perms = slices.Clone(perms)
}

// AddZone stores a zone and answers its id.
func (s *Server) AddZone(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	z := zone{id: fmt.Sprintf("%032x", s.seq), name: name}
	s.zones = append(s.zones, z)
	return z.id
}

// route registers h behind the guard at path and at the same path under the API prefix.
func (s *Server) route(method, path, perm string, h http.HandlerFunc) {
	guarded := func(w http.ResponseWriter, r *http.Request) {
		if status, code, message := s.guard(r, perm); status != 0 {
			writeError(w, status, code, message)
			return
		}
		h(w, r)
	}
	s.cloud.Handle(method, path, guarded)
	s.cloud.Handle(method, apiPrefix+path, guarded)
}

// guard answers the refusal for r, or a zero status when r may proceed.
func (s *Server) guard(r *http.Request, perm string) (status, code int, message string) {
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); !ok || token != Token {
		return http.StatusUnauthorized, codeInvalid, "Invalid API Token"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if a := r.PathValue("a"); a != "" && a != AccountID {
		return http.StatusForbidden, codeForbidden, "Authentication error"
	}
	if perm != "" && !slices.Contains(s.perms, perm) {
		return http.StatusForbidden, codeForbidden, "Authentication error"
	}
	return 0, 0, ""
}

func (s *Server) verifyToken(w http.ResponseWriter, _ *http.Request) {
	writeResult(w, map[string]any{"id": "fake-token-id", "status": "active"})
}

func (s *Server) listZones(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []any{}
	for _, z := range s.zones {
		if name != "" && z.name != name {
			continue
		}
		out = append(out, map[string]any{
			"id": z.id, "name": z.name, "status": "active",
			"account": map[string]any{"id": AccountID, "name": "Fake Account"},
		})
	}
	writeList(w, out)
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type resultInfo struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	Count      int `json:"count"`
	TotalCount int `json:"total_count"`
	TotalPages int `json:"total_pages"`
}

type envelope struct {
	Success    bool        `json:"success"`
	Errors     []apiError  `json:"errors"`
	Messages   []string    `json:"messages"`
	Result     any         `json:"result"`
	ResultInfo *resultInfo `json:"result_info,omitempty"`
}

func writeEnvelope(w http.ResponseWriter, status int, e envelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(e)
}

func writeResult(w http.ResponseWriter, result any) {
	writeEnvelope(w, http.StatusOK, envelope{Success: true, Errors: []apiError{}, Messages: []string{}, Result: result})
}

// writeList answers items, which must be non-nil so the result renders as an array.
func writeList(w http.ResponseWriter, items []any) {
	info := &resultInfo{Page: 1, PerPage: max(len(items), perPageMin), Count: len(items), TotalCount: len(items), TotalPages: 1}
	writeEnvelope(w, http.StatusOK, envelope{Success: true, Errors: []apiError{}, Messages: []string{}, Result: items, ResultInfo: info})
}

func writeError(w http.ResponseWriter, status, code int, message string) {
	writeEnvelope(w, status, envelope{Errors: []apiError{{Code: code, Message: message}}, Messages: []string{}})
}

// decodeBody reads the whole request body as one JSON document into v.
func decodeBody(r *http.Request, v any) error {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

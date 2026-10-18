package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

func TestHealthz(t *testing.T) {
	h := newHarness(t)
	session, csrf := h.login()
	readToken := h.token(core.ScopeRead)
	var recs []*httptest.ResponseRecorder
	detail := fmt.Sprintf(`{"status":"ok","version":%q,"commit":%q,"schemaVersion":3}`, buildinfo.Version, buildinfo.Commit)
	for _, tc := range []struct {
		name string
		body string
		r    func() *http.Request
	}{
		{"no credentials", `{"status":"ok"}`, func() *http.Request { return request("GET", "/healthz", "") }},
		{"invalid bearer", `{"status":"ok"}`, func() *http.Request { return withToken(request("GET", "/healthz", ""), "dpt_bogus") }},
		{"invalid session", `{"status":"ok"}`, func() *http.Request { return withSession(request("GET", "/healthz", ""), "bogus", "") }},
		{"read token", detail, func() *http.Request { return withToken(request("GET", "/healthz", ""), readToken) }},
		{"session", detail, func() *http.Request { return withSession(request("GET", "/healthz", ""), session, "") }},
	} {
		rec := h.serve(tc.r())
		recs = append(recs, rec)
		if rec.Code != 200 || rec.Body.String() != tc.body || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %s %v, want 200 %s", tc.name, rec.Code, rec.Body, rec.Header(), tc.body)
		}
	}
	head := h.serve(request("HEAD", "/healthz", ""))
	recs = append(recs, head)
	if head.Code != 200 {
		t.Errorf("HEAD /healthz: %d", head.Code)
	}

	h.srv.cfg.SchemaVersion = func(context.Context) (int, error) { return 0, errors.New("database is locked") }
	failing := h.serve(withSession(request("GET", "/healthz", ""), session, ""))
	recs = append(recs, failing)
	decodeProblem(t, failing, 500, errcode.Internal)
	public := h.serve(request("GET", "/healthz", ""))
	recs = append(recs, public)
	if public.Code != 200 || public.Body.String() != `{"status":"ok"}` {
		t.Errorf("public health with a failing database: %d %s", public.Code, public.Body)
	}
	noCredentials(t, h, recs, map[string]string{"session id": session, "CSRF token": csrf, "read token secret": tokenSecret(readToken),
		"invalid bearer token": "dpt_bogus", "invalid session id": "bogus"})
}

func TestOpenAPIRoute(t *testing.T) {
	h := newHarness(t)
	unauthenticated := h.serve(request("GET", "/api/v1/openapi.json", ""))
	decodeProblem(t, unauthenticated, 401, errcode.AuthRequired)

	readToken := h.token(core.ScopeRead)
	rec := h.serve(withToken(request("GET", "/api/v1/openapi.json", ""), readToken))
	noCredentials(t, h, []*httptest.ResponseRecorder{unauthenticated, rec}, map[string]string{"read token secret": tokenSecret(readToken)})
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	var doc struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc.OpenAPI != "3.1.0" {
		t.Fatalf("document: %v %q", err, doc.OpenAPI)
	}
	for path, method := range map[string]string{
		"/healthz":                     "get",
		"/api/v1/auth/login":           "post",
		"/api/v1/openapi.json":         "get",
		"/api/v1/changes/{id}/confirm": "post",
		"/api/v1/hosts/{host}/actions/demo.item.add/v1/plan": "post",
		"/api/v1/actions/demo.item.list/v1/read":             "post",
		"/api/v1/hosts/{host}/resources/demo/status":         "get",
	} {
		if _, ok := doc.Paths[path][method]; !ok {
			t.Errorf("document lacks %s %s", method, path)
		}
	}
	for _, path := range []string{"/api/v1/actions/demo.secret.reset/v1/plan", "/api/v1/actions/demo.secret.show/v1/read",
		"/api/v1/hosts/{host}/actions/{name}/{version}/plan"} {
		if _, ok := doc.Paths[path]; ok {
			t.Errorf("document has %s", path)
		}
	}
}

func TestRoutes(t *testing.T) {
	want := []schema.Route{
		{Method: "POST", Path: "/api/v1/auth/login", Public: true},
		{Method: "POST", Path: "/api/v1/auth/logout"},
		{Method: "GET", Path: "/api/v1/auth/session"},
		{Method: "GET", Path: "/api/v1/changes"},
		{Method: "GET", Path: "/api/v1/changes/{id}"},
		{Method: "POST", Path: "/api/v1/changes/{id}/confirm"},
		{Method: "POST", Path: "/api/v1/changes/{id}/revert"},
		{Method: "GET", Path: "/healthz", Public: true},
		{Method: "GET", Path: "/api/v1/openapi.json"},
	}
	got := Routes()
	if len(got) != len(want) {
		t.Fatalf("Routes() = %+v, want %d routes", got, len(want))
	}
	for i, r := range got {
		if r.Method != want[i].Method || r.Path != want[i].Path || r.Public != want[i].Public || r.Summary == "" {
			t.Errorf("route %d = %+v, want %+v with a summary", i, r, want[i])
		}
		if r.Public != (routeAccess(r.Method, r.Path) != accessAuthenticated) {
			t.Errorf("%s %s: Public %v disagrees with routeAccess", r.Method, r.Path, r.Public)
		}
	}
}

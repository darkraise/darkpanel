package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// An unhealthy controller still answers 200, because the update's health
// probe reads any other status as "not yet" and would wait out its deadline
// instead of failing at once.
func TestHealthzStatus(t *testing.T) {
	h := newHarness(t)
	readToken := h.token(core.ScopeRead)
	for _, tc := range []struct{ config, want string }{
		{"", "ok"},
		{"degraded", "degraded"},
	} {
		h.srv.cfg.HealthStatus = tc.config
		detail := fmt.Sprintf(`{"status":%q,"version":%q,"commit":%q,"schemaVersion":3}`, tc.want, buildinfo.Version, buildinfo.Commit)
		for _, answer := range []struct {
			name string
			rec  *httptest.ResponseRecorder
			body string
		}{
			{"bare", h.serve(request("GET", "/healthz", "")), fmt.Sprintf(`{"status":%q}`, tc.want)},
			{"detail", h.serve(withToken(request("GET", "/healthz", ""), readToken)), detail},
			{"socket", h.socketRequest(request("GET", "/healthz", ""), 0), detail},
		} {
			if answer.rec.Code != http.StatusOK || answer.rec.Body.String() != answer.body {
				t.Errorf("HealthStatus %q, %s answer: %d %s, want 200 %s", tc.config, answer.name, answer.rec.Code, answer.rec.Body, answer.body)
			}
		}
	}
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
		"/healthz":                                           "get",
		"/api/v1/auth/login":                                 "post",
		"/api/v1/openapi.json":                               "get",
		"/api/v1/controller/listeners":                       "get",
		"/api/v1/changes/{id}/confirm":                       "post",
		"/api/v1/hosts/{host}/actions/demo.item.add/v1/plan": "post",
		"/api/v1/actions/demo.item.list/v1/read":             "post",
		"/api/v1/hosts/{host}/resources/demo/status":         "get",
		"/api/v1/hosts/{host}/modules/{module}/availability": "get",
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
		{Method: "GET", Path: "/api/v1/hosts/{host}/modules/{module}/availability"},
		{Method: "GET", Path: "/api/v1/changes"},
		{Method: "GET", Path: "/api/v1/changes/{id}"},
		{Method: "POST", Path: "/api/v1/changes/{id}/confirm"},
		{Method: "POST", Path: "/api/v1/changes/{id}/revert"},
		{Method: "POST", Path: "/api/v1/changes/{id}/undo"},
		{Method: "POST", Path: "/api/v1/changes/{id}/repair"},
		{Method: "POST", Path: "/api/v1/changes/{id}/release"},
		{Method: "GET", Path: "/api/v1/changes/{id}/effects"},
		{Method: "GET", Path: "/api/v1/changes/{id}/effects/{seq}/observe"},
		{Method: "POST", Path: "/api/v1/changes/{id}/effects/{seq}/resolve"},
		{Method: "GET", Path: "/api/v1/changes/{id}/steps"},
		{Method: "GET", Path: "/healthz", Public: true},
		{Method: "GET", Path: "/api/v1/openapi.json"},
		{Method: "GET", Path: "/api/v1/controller/listeners"},
		{Method: "GET", Path: "/api/v1/events"},
		{Method: "GET", Path: "/api/v1/hosts/{host}/streams/{module}/{name}"},
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

func TestListenersRoute(t *testing.T) {
	h := newHarness(t)
	session, csrf := h.login()
	readToken := h.token(core.ScopeRead)
	const path = "/api/v1/controller/listeners"
	var recs []*httptest.ResponseRecorder

	rec := h.serve(request("GET", path, ""))
	recs = append(recs, rec)
	decodeProblem(t, rec, 401, errcode.AuthRequired)

	rec = h.serve(withToken(request("GET", path, ""), readToken))
	recs = append(recs, rec)
	if want := `{"tailscaleBound":false,"message":"The listeners are not started yet."}`; rec.Code != 200 || rec.Body.String() != want ||
		rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("before the listeners start: %d %s %v, want 200 %s", rec.Code, rec.Body, rec.Header(), want)
	}

	h.srv.SetListenerState(func() ListenerState {
		return ListenerState{TailscaleBound: true, Message: "Serving on Tailscale: https://100.64.0.1:8443."}
	})
	want := `{"tailscaleBound":true,"message":"Serving on Tailscale: https://100.64.0.1:8443."}`
	for name, r := range map[string]*http.Request{
		"read token": withToken(request("GET", path, ""), readToken),
		"session":    withSession(request("GET", path, ""), session, ""),
	} {
		rec := h.serve(r)
		recs = append(recs, rec)
		if rec.Code != 200 || rec.Body.String() != want {
			t.Errorf("%s: %d %s, want 200 %s", name, rec.Code, rec.Body, want)
		}
	}

	rec = h.serve(withToken(request("POST", path, ""), readToken))
	recs = append(recs, rec)
	decodeProblem(t, rec, http.StatusMethodNotAllowed, errcode.MethodNotAllowed)
	if rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("Allow = %q, want GET, HEAD", rec.Header().Get("Allow"))
	}
	noCredentials(t, h, recs, map[string]string{"session id": session, "CSRF token": csrf, "read token secret": tokenSecret(readToken)})
}

// TestListenersRouteAnswersHEAD runs on a real server, because a
// ResponseRecorder keeps the body net/http drops from a HEAD response.
func TestListenersRouteAnswersHEAD(t *testing.T) {
	h := newHarness(t)
	srv := httptest.NewServer(h.srv.Handler())
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, srv.URL+"/api/v1/controller/listeners", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+h.token(core.ScopeRead))
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || len(body) != 0 || res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("HEAD = %d with %d body bytes and Cache-Control %q; want 200, no body, no-store",
			res.StatusCode, len(body), res.Header.Get("Cache-Control"))
	}
}

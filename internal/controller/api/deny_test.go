package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// concrete fills a route pattern's wildcards with values the demo registry
// knows.
var concrete = strings.NewReplacer("{host}", "h1", "{name}", "demo.item.add", "{version}", "v1",
	"{module}", "demo", "{resource}", "status", "{id}", "c7")

// unknownPaths match no route.
var unknownPaths = []string{"/api", "/api/", "/api/v1/nope", "/api/v2/changes", "/api/v1/hosts/h1/nope",
	"/api/v1/changes/c7/explode", "/api/v1/hosts/h1/actions/demo.item.add/v1/draft"}

// specPublic is the public list of spec section 12, written out rather than
// taken from routeAccess so that a mistake there fails this test.
func specPublic(method, path string) bool {
	get := method == "GET" || method == "HEAD"
	switch path {
	case "/api/v1/auth/login":
		return method == "POST"
	case "/healthz", "/", "/hosts/h1/hostinfo", "/assets/app.js":
		return get
	}
	return false
}

// TestDenyByDefault is spec section 12's requirement against the real mux:
// every method on every route that is not public, and on every path that
// matches no route, refuses a request without valid credentials, and nothing
// is dispatched.
func TestDenyByDefault(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	session, _ := h.login()

	var targets []string
	for _, rt := range h.srv.networkRoutes() {
		targets = append(targets, concrete.Replace(rt.path))
	}
	targets = append(targets, unknownPaths...)
	targets = append(targets, "/", "/hosts/h1/hostinfo", "/assets/app.js")
	for _, path := range targets {
		for _, m := range allMethods {
			if specPublic(m, path) {
				continue
			}
			for name, r := range map[string]func() *http.Request{
				"no credentials": func() *http.Request { return request(m, path, `{"input":{}}`) },
				"invalid bearer": func() *http.Request { return withToken(request(m, path, `{"input":{}}`), "dpt_01J_bogus") },
				"invalid cookie": func() *http.Request { return withSession(request(m, path, `{"input":{}}`), "bogus", "") },
			} {
				last = call{}
				rec := h.serve(r())
				decodeProblem(t, rec, 401, errcode.AuthRequired)
				if last.method != "" {
					t.Errorf("%s %s with %s: dispatched %s", m, path, name, last.method)
				}
			}
			if m != "GET" && m != "HEAD" && m != "OPTIONS" {
				last = call{}
				decodeProblem(t, h.serve(withSession(request(m, path, `{"input":{}}`), session, "")), 403, errcode.CSRF)
				if last.method != "" {
					t.Errorf("%s %s with a session and no CSRF token: dispatched %s", m, path, last.method)
				}
			}
		}
	}
}

// TestUnroutedAfterAuthentication shows that not_found and
// method_not_allowed come only after authentication.
func TestUnroutedAfterAuthentication(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	token := h.token(core.ScopeRead)
	for _, path := range unknownPaths {
		for _, m := range allMethods {
			decodeProblem(t, h.serve(withToken(request(m, path, ""), token)), 404, errcode.NotFound)
		}
	}
	for path, allow := range map[string]string{
		"/api/v1/changes":                                "GET, HEAD",
		"/api/v1/changes/c7/confirm":                     "POST",
		"/api/v1/hosts/h1/actions/demo.item.add/v1/plan": "POST",
		"/api/v1/auth/login":                             "POST",
		"/healthz":                                       "GET, HEAD",
		"/hosts/h1":                                      "GET, HEAD",
	} {
		rec := h.serve(withToken(request("PATCH", path, ""), token))
		decodeProblem(t, rec, 405, errcode.MethodNotAllowed)
		if got := rec.Header().Get("Allow"); got != allow {
			t.Errorf("PATCH %s: Allow %q, want %q", path, got, allow)
		}
	}
	if last.method != "" {
		t.Errorf("dispatched %s", last.method)
	}
}

// TestBreakGlassRefusedOnTheNetwork checks every break-glass action of the
// registry with an admin token.
func TestBreakGlassRefusedOnTheNetwork(t *testing.T) {
	h := newHarnessWith(t, arvModule{})
	var last call
	h.stubDispatcher(&last)
	h.disp.RevealFunc = func(context.Context, core.Principal, string, core.ActionRef, json.RawMessage) (json.RawMessage, error) {
		last.method = "Reveal"
		return nil, nil
	}
	token := h.token(core.ScopeAdmin)
	n := 0
	for _, def := range h.srv.cfg.Registry.Actions() {
		if !def.Spec.BreakGlass {
			continue
		}
		ops := []string{opPlan, opApply}
		switch {
		case def.Spec.Reveal:
			ops = []string{opReveal}
		case def.Spec.Class == core.ClassRead:
			ops = []string{opRead}
		}
		for _, op := range ops {
			n++
			path := "/api/v1/actions/" + def.Spec.Name + "/v1/" + op
			decodeProblem(t, h.serve(withToken(request("POST", path, `{"input":{},"planId":"p"}`), token)), 403, errcode.Forbidden)
		}
	}
	if n != 4 || last.method != "" {
		t.Errorf("checked %d break-glass operations, dispatched %q; want 4 and none", n, last.method)
	}
}

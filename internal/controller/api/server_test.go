package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var testStart = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

const (
	testPassword = "correct horse battery"
	testHostID   = "01HOST0000000000000000000A"
	testRemote   = "192.0.2.10:51234"
)

// testParams keep argon2id cheap in tests.
var testParams = auth.Params{Memory: 8, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}

type demoInput struct {
	Name string `json:"name" jsonschema:"pattern=^[a-z]+$"`
}

type demoOutput struct {
	OK bool `json:"ok"`
}

type demoStatus struct {
	Healthy bool `json:"healthy"`
}

// demoHandler implements every handler interface and is never called: the
// tests' dispatcher is a fake.
type demoHandler[In, Out any] struct{}

func (demoHandler[In, Out]) Read(context.Context, core.Env, In) (Out, error) {
	var out Out
	return out, nil
}

func (demoHandler[In, Out]) Plan(context.Context, core.Env, In) (core.PlanOutcome, error) {
	return core.PlanOutcome{}, nil
}

func (demoHandler[In, Out]) Apply(context.Context, core.Env, In) (Out, error) {
	var out Out
	return out, nil
}

func (demoHandler[In, Out]) Verify(context.Context, core.Env, In, Out) error { return nil }

func (demoHandler[In, Out]) Snapshot(context.Context, core.Env, In) ([]byte, error) { return nil, nil }

func (demoHandler[In, Out]) Revert(context.Context, core.Env, []byte) error { return nil }

// demoModule has an agent-side read, revertible and irreversible action, a
// controller-side read and compensable action, two break-glass actions and
// a resource.
type demoModule struct{}

func (demoModule) Manifest() core.Manifest {
	h := demoHandler[demoInput, demoOutput]{}
	return core.Manifest{
		ID: "demo", Version: 1,
		Actions: []core.ActionDef{
			core.DefineRead(core.ActionSpec{Name: "demo.item.get", Version: 1, Side: core.SideAgent}, h),
			core.DefineRevertible(core.ActionSpec{Name: "demo.item.add", Version: 1, Side: core.SideAgent, LockKeys: []string{"demo"}}, h),
			core.DefineMutating(core.ActionSpec{Name: "demo.item.purge", Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent}, h),
			core.DefineRead(core.ActionSpec{Name: "demo.item.list", Version: 1, Side: core.SideController}, h),
			core.DefineMutating(core.ActionSpec{Name: "demo.item.remove", Version: 1, Class: core.ClassCompensable,
				Side: core.SideController, CompensatedBy: "demo.item.add"}, h),
			core.DefineMutating(core.ActionSpec{Name: "demo.secret.reset", Version: 1, Class: core.ClassIrreversible,
				Side: core.SideController, BreakGlass: true}, h),
			core.DefineRead(core.ActionSpec{Name: "demo.secret.show", Version: 1, Side: core.SideController, BreakGlass: true}, h),
		},
		Resources: []core.ResourceDef{
			core.DefineResource("demo.status", func(context.Context, core.Env) (demoStatus, error) {
				return demoStatus{}, nil
			}),
		},
	}
}

type fakeWebCA struct{}

func (fakeWebCA) RootPEM() []byte {
	return []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n")
}

func (fakeWebCA) Fingerprint() string { return strings.Repeat("ab", 32) }

// harness is a server over a real auth service with one active admin, a
// fake dispatcher and demoModule's registry.
type harness struct {
	t      *testing.T
	srv    *Server
	auth   *auth.Service
	clock  *coretest.FakeClock
	audit  *coretest.MemAudit
	disp   *coretest.FakeDispatcher
	user   auth.User
	secret string
	log    *bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{Side: store.SideController, BinaryVersion: "test",
		BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: testStart}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, clock: coretest.NewFakeClock(testStart), audit: &coretest.MemAudit{}, disp: &coretest.FakeDispatcher{}}
	h.auth = auth.New(db, coretest.Sealer{}, h.clock, h.audit, auth.Options{Params: testParams})
	if _, h.secret, err = h.auth.CreateUser(ctx, "admin", testPassword); err != nil {
		t.Fatal(err)
	}
	if h.user, err = h.auth.ActivateUser(ctx, "admin", h.code()); err != nil {
		t.Fatal(err)
	}
	reg, err := core.NewRegistry(demoModule{})
	if err != nil {
		t.Fatal(err)
	}
	log, buf := testLog()
	h.log = buf
	h.srv, err = New(Config{
		Dispatcher:    h.disp,
		Auth:          h.auth,
		Registry:      reg,
		WebCA:         fakeWebCA{},
		SchemaVersion: func(context.Context) (int, error) { return 3, nil },
		HostID:        testHostID,
		UI:            testUI,
		Log:           log,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// code advances the clock one TOTP period, so no code is used twice, and
// returns the admin's current code.
func (h *harness) code() string {
	h.t.Helper()
	h.clock.Advance(auth.TOTPPeriod)
	code, err := auth.TOTPCode(h.secret, h.clock.Now())
	if err != nil {
		h.t.Fatal(err)
	}
	return code
}

// wrongCode returns a six-digit code that matches none of the admin's steps
// a login accepts at the clock's time, without advancing the clock.
func (h *harness) wrongCode() string {
	h.t.Helper()
	var valid []string
	for _, step := range []time.Duration{-1, 0, 1} {
		c, err := auth.TOTPCode(h.secret, h.clock.Now().Add(step*auth.TOTPPeriod))
		if err != nil {
			h.t.Fatal(err)
		}
		valid = append(valid, c)
	}
	for d := '0'; d <= '9'; d++ {
		if c := strings.Repeat(string(d), 6); !slices.Contains(valid, c) {
			return c
		}
	}
	h.t.Fatal("no wrong code found")
	return ""
}

// login opens a session for the admin and returns its id and CSRF token.
func (h *harness) login() (session, csrf string) {
	h.t.Helper()
	res, err := h.auth.Login(context.Background(), auth.LoginRequest{
		Username: "admin", Password: testPassword, Code: h.code(), Source: "192.0.2.10"})
	if err != nil {
		h.t.Fatal(err)
	}
	return res.SessionID, res.CSRFToken
}

// token creates an API token of the admin with scope.
func (h *harness) token(scope core.Scope) string {
	h.t.Helper()
	_, tok, err := h.auth.CreateToken(context.Background(), h.user.ID, "test", scope, 0)
	if err != nil {
		h.t.Fatal(err)
	}
	return tok
}

func (h *harness) serve(r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, r)
	return rec
}

// request builds a request from testRemote; a non-empty body is sent as
// JSON.
func request(method, target, body string) *http.Request {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, target, rd)
	r.RemoteAddr = testRemote
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

// withSession adds the plain listener's session cookie and, when csrf is
// set, the CSRF header.
func withSession(r *http.Request, session, csrf string) *http.Request {
	r.Header.Add("Cookie", SessionCookiePlain+"="+session)
	if csrf != "" {
		r.Header.Set(CSRFHeader, csrf)
	}
	return r
}

func withToken(r *http.Request, token string) *http.Request {
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

// noCredentials fails t when a submitted credential appears in a response
// body or in the server's log. values maps each credential's role to its
// value; a failure names the role and never prints the value.
func noCredentials(t *testing.T, h *harness, recs []*httptest.ResponseRecorder, values map[string]string) {
	t.Helper()
	for role, v := range values {
		if v == "" {
			continue
		}
		for i, rec := range recs {
			if strings.Contains(rec.Body.String(), v) {
				t.Errorf("response %d contains the %s", i, role)
			}
		}
		if strings.Contains(h.log.String(), v) {
			t.Errorf("the log contains the %s", role)
		}
	}
}

// probe records whether it ran and with which caller.
type probe struct {
	ran    bool
	caller caller
	found  bool
}

func (p *probe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.ran = true
	p.caller, p.found = callerFrom(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (h *harness) through(r *http.Request) (*httptest.ResponseRecorder, *probe) {
	p := &probe{}
	rec := httptest.NewRecorder()
	h.srv.authenticate(p).ServeHTTP(rec, r)
	return rec, p
}

var allMethods = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions}

func TestRouteAccess(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         access
	}{
		{"POST", "/api/v1/auth/login", accessPublic},
		{"GET", "/api/v1/auth/login", accessAuthenticated},
		{"GET", "/healthz", accessOptional},
		{"HEAD", "/healthz", accessOptional},
		{"POST", "/healthz", accessAuthenticated},
		{"GET", "/", accessPublic},
		{"HEAD", "/assets/app.js", accessPublic},
		{"GET", "/hosts/01J/hostinfo", accessPublic},
		{"POST", "/hosts", accessAuthenticated},
		{"OPTIONS", "/", accessAuthenticated},
		{"GET", "/api", accessAuthenticated},
		{"GET", "/api/v1/openapi.json", accessAuthenticated},
		{"GET", "/api/v1/changes", accessAuthenticated},
		{"POST", "/api/v1/auth/login/", accessAuthenticated},
	} {
		if got := routeAccess(tc.method, tc.path); got != tc.want {
			t.Errorf("routeAccess(%s, %s) = %d, want %d", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestAuthenticateDeniesWithoutCredentials(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"/api/v1/changes", "/api/v1/nope", "/api", "/hosts"} {
		for _, m := range allMethods {
			if routeAccess(m, path) != accessAuthenticated {
				continue
			}
			rec, p := h.through(request(m, path, ""))
			if p.ran {
				t.Errorf("%s %s reached the handler without credentials", m, path)
			}
			decodeProblem(t, rec, 401, errcode.AuthRequired)
		}
	}
}

func TestAuthenticatePublicRoutes(t *testing.T) {
	h := newHarness(t)
	session, _ := h.login()
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/v1/auth/login"},
		{"GET", "/"},
		{"HEAD", "/assets/app.js"},
	} {
		for name, r := range map[string]*http.Request{
			"no credentials":  request(tc.method, tc.path, ""),
			"invalid bearer":  withToken(request(tc.method, tc.path, ""), "dpt_bogus"),
			"a valid session": withSession(request(tc.method, tc.path, ""), session, ""),
		} {
			rec, p := h.through(r)
			if !p.ran || p.found || rec.Code != http.StatusNoContent {
				t.Errorf("%s %s with %s: ran %v, caller %v, status %d; want served without a caller",
					tc.method, tc.path, name, p.ran, p.found, rec.Code)
			}
		}
	}
}

func TestAuthenticateHealthzIgnoresInvalidCredentials(t *testing.T) {
	h := newHarness(t)
	session, _ := h.login()
	token := h.token(core.ScopeRead)
	for _, tc := range []struct {
		name   string
		r      *http.Request
		caller string
	}{
		{"no credentials", request("GET", "/healthz", ""), ""},
		{"invalid bearer", withToken(request("GET", "/healthz", ""), "dpt_bogus"), ""},
		{"invalid session", withSession(request("HEAD", "/healthz", ""), "bogus", ""), ""},
		{"valid token", withToken(request("GET", "/healthz", ""), token), "token:"},
		{"valid session", withSession(request("GET", "/healthz", ""), session, ""), "user:" + h.user.ID},
	} {
		rec, p := h.through(tc.r)
		if !p.ran || rec.Code != http.StatusNoContent {
			t.Errorf("%s: ran %v, status %d; want served", tc.name, p.ran, rec.Code)
			continue
		}
		if tc.caller == "" && p.found {
			t.Errorf("%s: caller %+v, want none", tc.name, p.caller.principal)
		}
		if tc.caller != "" && (!p.found || !strings.HasPrefix(p.caller.principal.Actor, tc.caller)) {
			t.Errorf("%s: caller %+v, want actor %s...", tc.name, p.caller.principal, tc.caller)
		}
	}
}

func TestCredentialPrecedence(t *testing.T) {
	h := newHarness(t)
	session, csrf := h.login()
	token := h.token(core.ScopeOperate)

	var recs []*httptest.ResponseRecorder

	rec, p := h.through(withSession(withToken(request("GET", "/api/v1/changes", ""), "dpt_bogus"), session, csrf))
	recs = append(recs, rec)
	if p.ran {
		t.Error("an invalid bearer token fell back to the session cookie")
	}
	decodeProblem(t, rec, 401, errcode.AuthRequired)

	r := withSession(request("GET", "/api/v1/changes", ""), session, csrf)
	r.Header.Add("Authorization", "Bearer "+token)
	r.Header.Add("Authorization", "Bearer "+token)
	rec, p = h.through(r)
	recs = append(recs, rec)
	if p.ran {
		t.Error("two Authorization values were accepted")
	}
	decodeProblem(t, rec, 401, errcode.AuthRequired)

	rec, p = h.through(withSession(withToken(request("GET", "/api/v1/changes", ""), token), "bogus", ""))
	recs = append(recs, rec)
	if !p.found || !strings.HasPrefix(p.caller.principal.Actor, "token:") || p.caller.principal.Scope != core.ScopeOperate ||
		p.caller.identity.FromSession || p.caller.session != "" {
		t.Errorf("a valid token with a stale cookie: caller %+v, want the token", p.caller)
	}

	noCredentials(t, h, recs, map[string]string{
		"session id": session, "CSRF token": csrf, "API token": token, "invalid bearer token": "dpt_bogus"})
}

func TestCSRF(t *testing.T) {
	h := newHarness(t)
	session, csrf := h.login()
	token := h.token(core.ScopeAdmin)
	otherCSRF := h.auth.CSRFToken(strings.Repeat("A", 43))
	var recs []*httptest.ResponseRecorder
	for _, m := range allMethods {
		safe := m == "GET" || m == "HEAD" || m == "OPTIONS"
		for _, tc := range []struct {
			name string
			r    *http.Request
			ok   bool
		}{
			{"session without token", withSession(request(m, "/api/v1/changes", ""), session, ""), safe},
			{"session with a wrong token", withSession(request(m, "/api/v1/changes", ""), session, csrf+"x"), safe},
			{"session with another session's token", withSession(request(m, "/api/v1/changes", ""), session, otherCSRF), safe},
			{"session with its token", withSession(request(m, "/api/v1/changes", ""), session, csrf), true},
			{"bearer token without CSRF", withToken(request(m, "/api/v1/changes", ""), token), true},
		} {
			rec, p := h.through(tc.r)
			recs = append(recs, rec)
			if p.ran != tc.ok {
				t.Errorf("%s %s: ran %v, want %v (status %d)", m, tc.name, p.ran, tc.ok, rec.Code)
			}
			if !tc.ok {
				decodeProblem(t, rec, 403, errcode.CSRF)
			}
			if tc.ok && p.caller.identity.FromSession && p.caller.session != session {
				t.Errorf("%s %s: caller session %q, want the cookie's value", m, tc.name, p.caller.session)
			}
		}
	}
	noCredentials(t, h, recs, map[string]string{
		"session id": session, "CSRF token": csrf, "API token": token, "other session's CSRF token": otherCSRF})
}

func TestSessionCookiePerListener(t *testing.T) {
	h := newHarness(t)
	session, _ := h.login()
	for _, tc := range []struct {
		name   string
		tls    bool
		cookie string
		ok     bool
	}{
		{"plain listener, plain cookie", false, SessionCookiePlain, true},
		{"plain listener, secure cookie", false, SessionCookieSecure, false},
		{"HTTPS listener, secure cookie", true, SessionCookieSecure, true},
		{"HTTPS listener, plain cookie", true, SessionCookiePlain, false},
	} {
		r := request("GET", "/api/v1/changes", "")
		if tc.tls {
			r.TLS = &tls.ConnectionState{}
		}
		r.Header.Add("Cookie", tc.cookie+"="+session)
		rec, p := h.through(r)
		if p.ran != tc.ok {
			t.Errorf("%s: ran %v, want %v (status %d)", tc.name, p.ran, tc.ok, rec.Code)
		}
	}
}

func TestSourceAddress(t *testing.T) {
	h := newHarness(t)
	token := h.token(core.ScopeRead)
	for remote, want := range map[string]string{
		"192.0.2.10:51234":   "192.0.2.10",
		"[2001:db8::1]:8443": "2001:db8::1",
		"@":                  "@",
	} {
		r := withToken(request("GET", "/api/v1/changes", ""), token)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-For", "203.0.113.9")
		_, p := h.through(r)
		if p.caller.principal.Source != want {
			t.Errorf("RemoteAddr %q: source %q, want %q", remote, p.caller.principal.Source, want)
		}
	}
}

func TestRequireCaller(t *testing.T) {
	log, _ := testLog()
	p := &probe{}
	rec := httptest.NewRecorder()
	requireCaller(log, p).ServeHTTP(rec, request("GET", "/api/v1/changes", ""))
	if p.ran {
		t.Error("requireCaller ran the handler without a caller")
	}
	decodeProblem(t, rec, 401, errcode.AuthRequired)

	r := request("GET", "/api/v1/changes", "")
	r = r.WithContext(withCaller(r.Context(), caller{principal: core.Principal{Actor: "token:t1", Scope: core.ScopeRead}}))
	requireCaller(log, p).ServeHTTP(httptest.NewRecorder(), r)
	if !p.ran {
		t.Error("requireCaller refused a request with a caller")
	}
}

func TestFallback(t *testing.T) {
	h := newHarness(t)
	token := h.token(core.ScopeRead)

	rec := h.serve(request("GET", "/api/v1/nope", ""))
	decodeProblem(t, rec, 401, errcode.AuthRequired)
	if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("refusal lacks the security headers: %v", rec.Header())
	}
	decodeProblem(t, h.serve(request("POST", "/hosts", "")), 401, errcode.AuthRequired)

	decodeProblem(t, h.serve(withToken(request("GET", "/api/v1/nope", ""), token)), 404, errcode.NotFound)
	rec = h.serve(withToken(request("DELETE", "/hosts", ""), token))
	decodeProblem(t, rec, 405, errcode.MethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want GET, HEAD", got)
	}

	rec = h.serve(request("GET", "/hosts/01J/hostinfo", ""))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>Darkpanel</title>") {
		t.Errorf("UI fallback: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("Cache-Control") != "" {
		t.Errorf("UI response headers: %v", rec.Header())
	}

	h.srv.cfg.UI = nil
	decodeProblem(t, h.serve(request("GET", "/hosts", "")), 404, errcode.NotFound)
	decodeProblem(t, h.serve(withToken(request("DELETE", "/hosts", ""), token)), 404, errcode.NotFound)
}

func TestNewRequiresDependencies(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("New accepted an empty Config")
	}
}

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/auth/oidcfake"
	"github.com/darkraise/darkpanel/internal/controller/events"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Acceptance tests of the M10 foundations spec through the API's HTTP
// surface, over a real auth service and database with a fake dispatcher.

const qaBase = "https://panel.example"

func qaExec(t *testing.T, h *harness, q string, args ...any) {
	t.Helper()
	if _, err := h.db.Writer().Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func qaCount(t *testing.T, h *harness, q string, args ...any) (n int) {
	t.Helper()
	if err := h.db.Read().QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return
}

// qaRebuild serves h's current configuration, with its clock.
func qaRebuild(t *testing.T, h *harness) {
	t.Helper()
	h.cfg.Clock = h.clock
	srv, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.srv = srv
}

// qaGrantedUser creates and activates a member with grants ("<target>=<role>")
// and returns a session credential and the user id.
func qaGrantedUser(t *testing.T, h *harness, name string, grants ...string) (func(*http.Request) *http.Request, string) {
	t.Helper()
	ctx := context.Background()
	_, secret, err := h.auth.CreateUserWithRole(ctx, name, testPassword, "member")
	if err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(auth.TOTPPeriod)
	code, _ := auth.TOTPCode(secret, h.clock.Now())
	u, err := h.auth.ActivateUser(ctx, name, code)
	if err != nil {
		t.Fatal(err)
	}
	var gs []auth.Grant
	for _, g := range grants {
		target, role, _ := strings.Cut(g, "=")
		gs = append(gs, auth.Grant{Target: target, Role: role})
	}
	if _, err := h.auth.SetGrants(ctx, name, gs); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(auth.TOTPPeriod)
	code, _ = auth.TOTPCode(secret, h.clock.Now())
	res, err := h.auth.Login(ctx, auth.LoginRequest{Username: name, Password: testPassword, Code: code, Source: "192.0.2.10"})
	if err != nil {
		t.Fatal(err)
	}
	return func(r *http.Request) *http.Request { return withSession(r, res.SessionID, res.CSRFToken) }, u.ID
}

func qaHosts(t *testing.T, h *harness, hosts ...string) {
	t.Helper()
	for _, id := range hosts {
		qaExec(t, h, `INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, 0, '2026-10-17T12:00:00.000000000Z')`, id, id)
	}
}

func qaGroupOf(t *testing.T, h *harness, id string, hosts ...string) {
	t.Helper()
	qaExec(t, h, `INSERT INTO core_host_groups (id, name, created_at) VALUES (?, ?, '2026-10-17T12:00:00.000000000Z')`, id, "group "+id)
	for _, host := range hosts {
		qaExec(t, h, `INSERT INTO core_host_group_members (group_id, host_id) VALUES (?, ?)`, id, host)
	}
}

// qaMatrixModule has an agent-side revertible, compensable and irreversible
// action, one that needs admin, and controller-side reads: one classified
// fleet, one with no HostData.
type qaMatrixModule struct{}

func (qaMatrixModule) Manifest() core.Manifest {
	h := demoHandler[demoInput, demoOutput]{}
	return core.Manifest{ID: "qam", Version: 1, Actions: []core.ActionDef{
		core.DefineRead(core.ActionSpec{Name: "qam.thing.get", Version: 1, Side: core.SideAgent}, h),
		core.DefineRevertible(core.ActionSpec{Name: "qam.thing.rev", Version: 1, Side: core.SideAgent, LockKeys: []string{"qam"}, RepairedBy: "qam.thing.irr"}, h),
		core.DefineMutating(core.ActionSpec{Name: "qam.thing.irr", Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent, LockKeys: []string{"qam"}}, h),
		core.DefineMutating(core.ActionSpec{Name: "qam.thing.comp", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, CompensatedBy: "qam.thing.rev"}, h),
		core.DefineMutating(core.ActionSpec{Name: "qam.thing.adm", Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent, MinScope: core.ScopeAdmin}, h),
		core.DefineRead(core.ActionSpec{Name: "qam.fleet.get", Version: 1, Side: core.SideController, HostData: core.HostDataFleet}, h),
		core.DefineRead(core.ActionSpec{Name: "qam.unclassified.get", Version: 1, Side: core.SideController}, h),
	}}
}

func qaFake(h *harness) {
	d := h.disp
	d.ReadFunc = func(context.Context, core.Principal, string, core.ActionRef, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), nil
	}
	d.ResourceFunc = func(context.Context, core.Principal, string, string) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}
	d.PlanFunc = func(_ context.Context, _ core.Principal, hostID string, a core.ActionRef, _ json.RawMessage, _ core.PlanOptions) (core.PlanView, error) {
		return core.PlanView{PlanID: "p1", HostID: hostID, Action: a.Name, Version: a.Version}, nil
	}
	d.ApplyFunc = func(_ context.Context, _ core.Principal, hostID string, a core.ActionRef, _ string, _ core.ApplyOptions) (core.ChangeView, error) {
		return core.ChangeView{ID: "c-new", HostID: hostID, Action: a.Name, Version: a.Version, State: core.StatePending}, nil
	}
	h.cfg.Availability = func(context.Context, string, string) (core.ModuleAvailability, error) {
		return core.ModuleAvailability{}, nil
	}
}

func TestA2_A3_roles_on_a_group_host(t *testing.T) {
	h := newHarnessWith(t, qaMatrixModule{})
	qaFake(h)
	qaRebuild(t, h)
	qaHosts(t, h, "g1", "x1")
	qaGroupOf(t, h, "G", "g1")
	viewer, _ := qaGrantedUser(t, h, "viewer", "G=viewer")
	operator, _ := qaGrantedUser(t, h, "operator", "G=operator")
	do := func(cred func(*http.Request) *http.Request, method, path, body string) *httptest.ResponseRecorder {
		return h.serve(cred(request(method, path, body)))
	}
	const in = `{"input":{"name":"a"}}`
	act := func(host, name, op string) string { return "/api/v1/hosts/" + host + "/actions/" + name + "/v1/" + op }

	// A2: a viewer reads and is refused every mutating action with auth.forbidden.
	for _, p := range []string{"/api/v1/hosts/g1/resources/demo/status", "/api/v1/hosts/g1/modules/qam/availability"} {
		if rec := do(viewer, "GET", p, ""); rec.Code != 200 {
			t.Errorf("viewer GET %s = %d, want 200", p, rec.Code)
		}
	}
	if rec := do(viewer, "POST", act("g1", "qam.thing.get", "read"), in); rec.Code != 200 {
		t.Errorf("viewer read = %d %s, want 200", rec.Code, rec.Body)
	}
	for _, name := range []string{"qam.thing.rev", "qam.thing.irr", "qam.thing.comp", "qam.thing.adm"} {
		decodeProblem(t, do(viewer, "POST", act("g1", name, "plan"), in), 403, errcode.Forbidden)
		decodeProblem(t, do(viewer, "POST", act("g1", name, "apply"), `{"planId":"p1"}`), 403, errcode.Forbidden)
	}

	// A3: an operator plans and applies a revertible, a compensable and an
	// irreversible action there, and is refused the admin one.
	for _, name := range []string{"qam.thing.rev", "qam.thing.comp", "qam.thing.irr"} {
		if rec := do(operator, "POST", act("g1", name, "plan"), in); rec.Code != 200 {
			t.Errorf("operator plan %s = %d %s, want 200", name, rec.Code, rec.Body)
		}
		if rec := do(operator, "POST", act("g1", name, "apply"), `{"planId":"p1"}`); rec.Code != 200 {
			t.Errorf("operator apply %s = %d %s, want 200", name, rec.Code, rec.Body)
		}
		decodeProblem(t, do(operator, "POST", act("x1", name, "plan"), in), 404, errcode.NotFound)
	}
	decodeProblem(t, do(operator, "POST", act("g1", "qam.thing.adm", "plan"), in), 403, errcode.Forbidden)
	decodeProblem(t, do(operator, "POST", act("g1", "qam.thing.adm", "apply"), `{"planId":"p1"}`), 403, errcode.Forbidden)
}

func TestA49_unclassified_read_is_refused_to_members(t *testing.T) {
	h := newHarnessWith(t, qaMatrixModule{})
	qaFake(h)
	qaRebuild(t, h)
	qaHosts(t, h, "g1")
	qaGroupOf(t, h, "G", "g1")
	star, _ := qaGrantedUser(t, h, "star", "*=operator")
	viewer, _ := qaGrantedUser(t, h, "viewer", "G=viewer")
	adminToken := h.token(core.ScopeRead)
	const in = `{"input":{}}`
	for name, cred := range map[string]func(*http.Request) *http.Request{"operator on *": star, "viewer": viewer} {
		for _, a := range []string{"qam.unclassified.get", "qam.fleet.get"} {
			decodeProblem(t, h.serve(cred(request("POST", "/api/v1/actions/"+a+"/v1/read", in))), 403, errcode.Forbidden)
			_ = name
		}
	}
	// An admin's read token still reads both: unclassified means fleet principals only.
	for _, a := range []string{"qam.unclassified.get", "qam.fleet.get"} {
		if rec := h.serve(withToken(request("POST", "/api/v1/actions/"+a+"/v1/read", in), adminToken)); rec.Code != 200 {
			t.Errorf("an admin's read token on %s = %d %s, want 200", a, rec.Code, rec.Body)
		}
	}
}

func TestA56_streams_follow_access_at_the_recheck(t *testing.T) {
	h := newHarnessWith(t, qaMatrixModule{})
	qaHosts(t, h, "g1")
	qaGroupOf(t, h, "G", "g1")
	_, uid := qaGrantedUser(t, h, "viewer", "G=viewer")
	_, tok, err := h.auth.CreateToken(context.Background(), uid, "stream", core.ScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	ts, _, _ := relayServerOf(t, h, pr)
	t.Cleanup(func() { _ = pw.Close() })
	resp := relayGet(t, ts, "/api/v1/hosts/g1/streams/demo/logs", tok)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("host stream = %d", resp.StatusCode)
	}
	frames := make(chan string, 16)
	go func() {
		buf, _ := io.ReadAll(resp.Body)
		frames <- string(buf)
	}()
	// The member loses its access; the stream ends at the next recheck with revoked.
	qaExec(t, h, `DELETE FROM core_host_group_members WHERE host_id = 'g1'`)
	deadline := time.After(10 * time.Second)
	for {
		h.clock.Advance(SSERecheck)
		select {
		case body := <-frames:
			if !strings.Contains(body, `"reason":"revoked"`) {
				t.Errorf("the host stream ended with %q, want an end frame with reason revoked", body)
			}
			return
		case <-deadline:
			t.Fatal("the host stream did not end after the member lost its host")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestA5_events_stream_filters_hosts_at_the_recheck(t *testing.T) {
	h := newHarnessWith(t, qaMatrixModule{})
	qaHosts(t, h, "g1", "x1")
	qaGroupOf(t, h, "G", "g1")
	_, uid := qaGrantedUser(t, h, "viewer", "G=viewer")
	_, tok, err := h.auth.CreateToken(context.Background(), uid, "events", core.ScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}
	bus := newQABus()
	h.cfg.Events, h.cfg.Clock = bus.bus, h.clock
	srv, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	resp, rd := sseOpen(t, ts.URL, tok, "stale")
	defer func() { _ = resp.Body.Close() }()
	frames := make(chan string, 64)
	go func() {
		defer close(frames)
		var lines []string
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSuffix(line, "\n")
			switch {
			case line == "" && len(lines) > 0:
				frames <- strings.Join(lines, "\n")
				lines = nil
			case line == "", strings.HasPrefix(line, ":"):
			default:
				lines = append(lines, line)
			}
		}
	}()
	next := func(wait time.Duration) (string, bool) {
		select {
		case f, ok := <-frames:
			if !ok {
				return "<closed>", true
			}
			return f, true
		case <-time.After(wait):
			return "", false
		}
	}
	if first, _ := next(5 * time.Second); !strings.Contains(first, "event: reset") {
		t.Fatalf("first frame %q, want a reset", first)
	}
	bus.host("x1")
	bus.host("g1")
	got, _ := next(5 * time.Second)
	if !strings.Contains(got, "g1") || strings.Contains(got, "x1") {
		t.Fatalf("the events stream carried %q first, want only g1's event (x1's is not visible)", got)
	}
	// The member loses g1: at the next recheck the stream stops carrying it and stays open.
	qaExec(t, h, `DELETE FROM core_host_group_members WHERE host_id = 'g1'`)
	h.clock.Advance(SSERecheck)
	var seen []string
	for {
		f, ok := next(500 * time.Millisecond)
		if !ok {
			break
		}
		seen = append(seen, f)
	}
	for _, f := range seen {
		if strings.Contains(f, "event: end") || f == "<closed>" {
			t.Fatalf("the events stream ended (%q) though only a host was lost; frames %q", f, seen)
		}
	}
	bus.host("g1")
	bus.host("x1")
	for {
		f, ok := next(500 * time.Millisecond)
		if !ok {
			break
		}
		if strings.Contains(f, "g1") || strings.Contains(f, "x1") {
			t.Errorf("after the recheck the stream still carries %q", f)
		}
		if strings.Contains(f, "event: end") || f == "<closed>" {
			t.Fatalf("the events stream ended (%q) with the user still enabled", f)
		}
	}
	// Disabling the user ends the stream at the next recheck.
	if _, err := h.auth.SetDisabled(context.Background(), "viewer", true); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(SSERecheck)
	var tail string
	for {
		f, ok := next(2 * time.Second)
		if !ok {
			break
		}
		tail += f + "\n"
		if f == "<closed>" {
			break
		}
	}
	if !strings.Contains(tail, "end") && !strings.Contains(tail, "<closed>") {
		t.Errorf("the events stream did not end when its user was disabled (frames %q)", tail)
	}
}

func TestA1_migration_0017_keeps_users_and_credentials(t *testing.T) {
	// Credentials made on the schema before 0017 keep working after it.
	h := newHarnessWith(t, qaMatrixModule{})
	qaFake(h)
	qaRebuild(t, h)
	readTok, opTok := h.token(core.ScopeRead), h.token(core.ScopeOperate)
	sess, csrf := h.login()
	if n := qaCount(t, h, `SELECT count(*) FROM core_users WHERE role != 'admin'`); n != 0 {
		t.Errorf("%d users are not admin after migration", n)
	}
	for _, tb := range []string{"core_host_groups", "core_host_group_members", "core_user_grants", "core_user_enrollments"} {
		if n := qaCount(t, h, `SELECT count(*) FROM `+tb); n != 0 {
			t.Errorf("%s holds %d rows after migration, want none", tb, n)
		}
	}
	for name, cred := range map[string]func(*http.Request) *http.Request{
		"session":       func(r *http.Request) *http.Request { return withSession(r, sess, csrf) },
		"read token":    func(r *http.Request) *http.Request { return withToken(r, readTok) },
		"operate token": func(r *http.Request) *http.Request { return withToken(r, opTok) },
	} {
		for _, p := range []string{"/api/v1/controller/listeners", "/api/v1/openapi.json", "/healthz"} {
			if rec := h.serve(cred(request("GET", p, ""))); rec.Code != 200 {
				t.Errorf("an admin %s on %s = %d %s, want 200 (meta routes)", name, p, rec.Code, rec.Body)
			}
		}
		if rec := h.serve(cred(request("POST", "/api/v1/actions/qam.fleet.get/v1/read", `{"input":{}}`))); rec.Code != 200 {
			t.Errorf("an admin %s on a fleet read = %d %s, want 200", name, rec.Code, rec.Body)
		}
		if rec := h.serve(cred(request("POST", "/api/v1/actions/qam.unclassified.get/v1/read", `{"input":{}}`))); rec.Code != 200 {
			t.Errorf("an admin %s on a read with no HostData = %d %s, want 200", name, rec.Code, rec.Body)
		}
		qaHosts(t, h, "agent-"+strings.ReplaceAll(name, " ", ""))
		if rec := h.serve(cred(request("GET", "/api/v1/hosts/agent-"+strings.ReplaceAll(name, " ", "")+"/resources/demo/status", ""))); rec.Code != 200 {
			t.Errorf("an admin %s reading a host resource = %d %s, want 200", name, rec.Code, rec.Body)
		}
	}
	// Operate scope still plans, read scope still does not.
	rec := h.serve(withToken(request("POST", "/api/v1/hosts/"+testHostID+"/actions/qam.thing.rev/v1/plan", `{"input":{"name":"a"}}`), readTok))
	decodeProblem(t, rec, 403, errcode.Forbidden)
	if rec := h.serve(withToken(request("POST", "/api/v1/hosts/"+testHostID+"/actions/qam.thing.rev/v1/plan", `{"input":{"name":"a"}}`), opTok)); rec.Code != 200 {
		t.Errorf("an admin operate token planning an agent action = %d %s, want 200", rec.Code, rec.Body)
	}
}

// ---- OIDC -------------------------------------------------------------------

type qaOIDC struct {
	*harness
	idp *oidcfake.Provider
	n   int
}

func qaNewOIDC(t *testing.T, requireTOTP bool) *qaOIDC {
	t.Helper()
	h := newHarness(t)
	idp := oidcfake.New(t)
	h.auth = auth.New(h.db, coretest.Sealer{}, h.clock, h.audit, auth.Options{Params: testParams, OIDCTransport: idp.Transport()})
	h.cfg.Auth = h.auth
	qaRebuild(t, h)
	ct, err := coretest.Sealer{}.Seal("auth_settings.oidc_client_secret_ct.1", []byte("qa-oidc-secret"))
	if err != nil {
		t.Fatal(err)
	}
	qaExec(t, h, `INSERT INTO auth_settings (id, oidc_issuer, oidc_client_id, oidc_client_secret_ct, oidc_display_name, oidc_redirect_base, oidc_require_totp, updated_at)
		VALUES (1, ?, 'qa-client', ?, 'QA SSO', ?, ?, '2026-11-24T00:00:00Z')`, idp.URL(), ct, qaBase, requireTOTP)
	if _, err := h.auth.SetIdentity(context.Background(), "admin", "admin@example.com", ""); err != nil {
		t.Fatal(err)
	}
	return &qaOIDC{harness: h, idp: idp}
}

// claims are the default claims of a valid ID token; edit changes them.
func (o *qaOIDC) claims(nonce, sub, email string, edit func(map[string]any)) map[string]any {
	now := o.clock.Now()
	c := map[string]any{"iss": o.idp.URL(), "aud": "qa-client", "sub": sub, "email": email, "email_verified": true,
		"nonce": nonce, "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix()}
	if edit != nil {
		edit(c)
	}
	return c
}

// qaFlow is one start and callback.
type qaFlow struct {
	start, callback *httptest.ResponseRecorder
	stateCookie     string
	state, code     string
}

// begin runs the start and returns what a callback needs.
func (o *qaOIDC) begin(provider *oidcfake.Provider) qaFlow {
	o.t.Helper()
	start := o.serve(request("GET", qaBase+"/api/v1/auth/oidc/start", ""))
	if start.Code != http.StatusFound {
		o.t.Fatalf("start = %d %s", start.Code, start.Body)
	}
	loc, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		o.t.Fatal(err)
	}
	f := qaFlow{start: start, state: loc.Query().Get("state")}
	for _, c := range start.Result().Cookies() {
		if c.Path == auth.OIDCCallbackPath {
			f.stateCookie = c.Name + "=" + c.Value
		}
	}
	if f.stateCookie == "" || f.state == "" {
		o.t.Fatalf("start gave no state or no cookie scoped to the callback: %v", start.Header())
	}
	o.n++
	f.code = fmt.Sprintf("qa-code-%d", o.n)
	return f
}

// register has provider issue claims for the flow's code.
func (o *qaOIDC) register(provider *oidcfake.Provider, f qaFlow, c map[string]any, edit func(*oidcfake.Code)) {
	loc, _ := url.Parse(f.start.Header().Get("Location"))
	code := oidcfake.Code{Challenge: loc.Query().Get("code_challenge"), Claims: c}
	if edit != nil {
		edit(&code)
	}
	provider.Register(f.code, code)
}

func (o *qaOIDC) callback(f qaFlow) *httptest.ResponseRecorder {
	r := request("GET", qaBase+auth.OIDCCallbackPath+"?state="+url.QueryEscape(f.state)+"&code="+f.code, "")
	r.Header.Add("Cookie", f.stateCookie)
	return o.serve(r)
}

func qaSessionCookie(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieSecure && c.Value != "" && c.MaxAge >= 0 {
			return c.Value
		}
	}
	return ""
}

// signIn runs a whole sign-in of sub and email at the provider and returns the callback.
func (o *qaOIDC) signIn(sub, email string, edit func(map[string]any)) *httptest.ResponseRecorder {
	o.t.Helper()
	f := o.begin(o.idp)
	loc, _ := url.Parse(f.start.Header().Get("Location"))
	o.register(o.idp, f, o.claims(loc.Query().Get("nonce"), sub, email, edit), nil)
	return o.callback(f)
}

func (o *qaOIDC) username(sessionCookie string) string {
	o.t.Helper()
	r := request("GET", qaBase+"/api/v1/auth/session", "")
	r.Header.Add("Cookie", SessionCookieSecure+"="+sessionCookie)
	rec := o.serve(r)
	var out struct {
		User struct{ Username string } `json:"user"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.User.Username
}

func TestA13_oidc_links_and_pins_the_subject(t *testing.T) {
	o := qaNewOIDC(t, false)
	rec := o.signIn("sub-1", "Admin@Example.com", nil)
	sess := qaSessionCookie(rec)
	if rec.Code != 200 || sess == "" || o.username(sess) != "admin" {
		t.Fatalf("a verified, case-different email sign-in: %d, session %q, user %q; want admin signed in", rec.Code, sess, o.username(sess))
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("the callback page headers %v, want no-store and no-referrer", rec.Header())
	}
	var iss, sub string
	if err := o.db.Read().QueryRow(`SELECT oidc_issuer, oidc_subject FROM core_users WHERE username = 'admin'`).Scan(&iss, &sub); err != nil {
		t.Fatal(err)
	}
	if iss != o.idp.URL() || sub != "sub-1" {
		t.Errorf("pinned issuer %q and subject %q, want %q and sub-1", iss, sub, o.idp.URL())
	}
	// The same subject with a changed email still signs in.
	rec = o.signIn("sub-1", "renamed@elsewhere.example", nil)
	if s := qaSessionCookie(rec); s == "" || o.username(s) != "admin" {
		t.Errorf("a later sign-in with the same subject and a changed email: no session (page %q)", rec.Body)
	}
}

func TestA14_oidc_refusals_create_nothing(t *testing.T) {
	type tc struct {
		name string
		run  func(o *qaOIDC) *httptest.ResponseRecorder
	}
	cases := []tc{
		{"unverified email", func(o *qaOIDC) *httptest.ResponseRecorder {
			return o.signIn("s1", "admin@example.com", func(c map[string]any) { c["email_verified"] = false })
		}},
		{"unknown email", func(o *qaOIDC) *httptest.ResponseRecorder { return o.signIn("s1", "nobody@example.com", nil) }},
		{"wrong nonce", func(o *qaOIDC) *httptest.ResponseRecorder {
			return o.signIn("s1", "admin@example.com", func(c map[string]any) { c["nonce"] = "another" })
		}},
		{"wrong audience", func(o *qaOIDC) *httptest.ResponseRecorder {
			return o.signIn("s1", "admin@example.com", func(c map[string]any) { c["aud"] = "other-client" })
		}},
		{"expired token", func(o *qaOIDC) *httptest.ResponseRecorder {
			return o.signIn("s1", "admin@example.com", func(c map[string]any) { c["exp"] = o.clock.Now().Add(-time.Hour).Unix() })
		}},
		{"wrong issuer", func(o *qaOIDC) *httptest.ResponseRecorder {
			return o.signIn("s1", "admin@example.com", func(c map[string]any) { c["iss"] = "https://evil.example" })
		}},
		{"bad signature", func(o *qaOIDC) *httptest.ResponseRecorder {
			f := o.begin(o.idp)
			loc, _ := url.Parse(f.start.Header().Get("Location"))
			other := oidcfake.New(t)
			o.register(o.idp, f, o.claims(loc.Query().Get("nonce"), "s1", "admin@example.com", nil), func(c *oidcfake.Code) { c.Key = other.Key })
			return o.callback(f)
		}},
		{"missing state", func(o *qaOIDC) *httptest.ResponseRecorder {
			f := o.begin(o.idp)
			loc, _ := url.Parse(f.start.Header().Get("Location"))
			o.register(o.idp, f, o.claims(loc.Query().Get("nonce"), "s1", "admin@example.com", nil), nil)
			r := request("GET", qaBase+auth.OIDCCallbackPath+"?code="+f.code, "")
			r.Header.Add("Cookie", f.stateCookie)
			return o.serve(r)
		}},
		{"state without its cookie", func(o *qaOIDC) *httptest.ResponseRecorder {
			f := o.begin(o.idp)
			loc, _ := url.Parse(f.start.Header().Get("Location"))
			o.register(o.idp, f, o.claims(loc.Query().Get("nonce"), "s1", "admin@example.com", nil), nil)
			return o.serve(request("GET", qaBase+auth.OIDCCallbackPath+"?state="+f.state+"&code="+f.code, ""))
		}},
		{"a state of another start", func(o *qaOIDC) *httptest.ResponseRecorder {
			f := o.begin(o.idp)
			g := o.begin(o.idp)
			loc, _ := url.Parse(f.start.Header().Get("Location"))
			o.register(o.idp, f, o.claims(loc.Query().Get("nonce"), "s1", "admin@example.com", nil), nil)
			f.state = g.state
			return o.callback(f)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := qaNewOIDC(t, false)
			users, sessions := qaCount(t, o.harness, `SELECT count(*) FROM core_users`), qaCount(t, o.harness, `SELECT count(*) FROM core_sessions`)
			rec := c.run(o)
			if s := qaSessionCookie(rec); s != "" {
				t.Errorf("%s: a session cookie was set", c.name)
			}
			if n := qaCount(t, o.harness, `SELECT count(*) FROM core_sessions`); n != sessions {
				t.Errorf("%s: sessions %d -> %d", c.name, sessions, n)
			}
			if n := qaCount(t, o.harness, `SELECT count(*) FROM core_users`); n != users {
				t.Errorf("%s: users %d -> %d", c.name, users, n)
			}
			if strings.Contains(rec.Body.String(), oidcfake.LeakMarker) || strings.Contains(o.log.String(), oidcfake.LeakMarker) {
				t.Errorf("%s: the provider's body reached the page or the log", c.name)
			}
		})
	}
	t.Run("a different subject for a pinned user", func(t *testing.T) {
		o := qaNewOIDC(t, false)
		if qaSessionCookie(o.signIn("s-original", "admin@example.com", nil)) == "" {
			t.Fatal("the first sign-in failed")
		}
		sessions := qaCount(t, o.harness, `SELECT count(*) FROM core_sessions`)
		rec := o.signIn("s-attacker", "admin@example.com", nil)
		if qaSessionCookie(rec) != "" || qaCount(t, o.harness, `SELECT count(*) FROM core_sessions`) != sessions {
			t.Errorf("a different subject signed in as the pinned user")
		}
	})
	t.Run("a replayed state", func(t *testing.T) {
		o := qaNewOIDC(t, false)
		f := o.begin(o.idp)
		loc, _ := url.Parse(f.start.Header().Get("Location"))
		o.register(o.idp, f, o.claims(loc.Query().Get("nonce"), "s1", "admin@example.com", nil), nil)
		if qaSessionCookie(o.callback(f)) == "" {
			t.Fatal("the first callback failed")
		}
		sessions := qaCount(t, o.harness, `SELECT count(*) FROM core_sessions`)
		f.code = f.code + "-again"
		o.register(o.idp, f, o.claims(loc.Query().Get("nonce"), "s1", "admin@example.com", nil), nil)
		if rec := o.callback(f); qaSessionCookie(rec) != "" || qaCount(t, o.harness, `SELECT count(*) FROM core_sessions`) != sessions {
			t.Errorf("a replayed state created a second session")
		}
	})
	t.Run("a colliding sub after the issuer changes", func(t *testing.T) {
		o := qaNewOIDC(t, false)
		if qaSessionCookie(o.signIn("shared-sub", "admin@example.com", nil)) == "" {
			t.Fatal("the first sign-in failed")
		}
		// The admin moves OIDC to another provider, which hands out the same sub.
		idp2 := oidcfake.New(t)
		first := o.idp
		route := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == strings.TrimPrefix(idp2.URL(), "https://") {
				return idp2.Transport().RoundTrip(r)
			}
			return first.Transport().RoundTrip(r)
		})
		o.harness.auth = auth.New(o.db, coretest.Sealer{}, o.clock, o.audit, auth.Options{Params: testParams, OIDCTransport: route})
		o.cfg.Auth = o.harness.auth
		qaRebuild(t, o.harness)
		qaExec(t, o.harness, `UPDATE auth_settings SET oidc_issuer = ?`, idp2.URL())
		o.idp = idp2
		sessions := qaCount(t, o.harness, `SELECT count(*) FROM core_sessions`)
		users := qaCount(t, o.harness, `SELECT count(*) FROM core_users`)
		rec := o.signIn("shared-sub", "admin@example.com", nil)
		if qaSessionCookie(rec) != "" || qaCount(t, o.harness, `SELECT count(*) FROM core_sessions`) != sessions {
			t.Errorf("the same sub at a new issuer signed in as the pinned user")
		}
		if qaCount(t, o.harness, `SELECT count(*) FROM core_users`) != users {
			t.Errorf("a user was created")
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestA15_oidc_require_totp_asks_for_the_code_first(t *testing.T) {
	o := qaNewOIDC(t, true)
	rec := o.signIn("sub-1", "admin@example.com", nil)
	if qaSessionCookie(rec) != "" || qaCount(t, o.harness, `SELECT count(*) FROM core_sessions`) != 0 {
		t.Fatal("a session exists before the code")
	}
	var step *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Value != "" && c.Name != SessionCookieSecure && c.Path == "/api/v1/auth/totp" {
			step = c
		}
	}
	if step == nil {
		t.Fatalf("the callback set no code-step cookie for /api/v1/auth/totp: %v", rec.Header())
	}
	post := func(code string) *httptest.ResponseRecorder {
		r := request("POST", qaBase+"/api/v1/auth/totp", `{"code":"`+code+`"}`)
		r.Header.Add("Cookie", step.Name+"="+step.Value)
		return o.serve(r)
	}
	if bad := post(o.wrongCode()); bad.Code == 200 || qaCount(t, o.harness, `SELECT count(*) FROM core_sessions`) != 0 {
		t.Errorf("a wrong code created a session: %d %s", bad.Code, bad.Body)
	}
	if ok := post(o.code()); ok.Code != 200 || qaCount(t, o.harness, `SELECT count(*) FROM core_sessions`) != 1 {
		t.Errorf("the right code: %d %s, want a session", ok.Code, ok.Body)
	}
}

func TestA11_A66_first_oidc_sign_in_activates_an_invite(t *testing.T) {
	// Without requireTotp the first matching sign-in activates; password login stays refused.
	o := qaNewOIDC(t, false)
	inv, err := o.auth.Invite(context.Background(), auth.InviteRequest{Username: "invitee", Role: "member", Email: "invitee@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	rec := o.signIn("inv-sub", "invitee@example.com", nil)
	sess := qaSessionCookie(rec)
	if sess == "" || o.username(sess) != "invitee" {
		t.Fatalf("an invited user's first matching OIDC sign-in: no session (page %q)", rec.Body)
	}
	if st := qaCount(t, o.harness, `SELECT count(*) FROM core_users WHERE username = 'invitee' AND state = 'active'`); st != 1 {
		t.Errorf("the invited user is not active after its first OIDC sign-in")
	}
	for _, pw := range []string{testPassword, inv.Token} {
		rec := o.serve(request("POST", "/api/v1/auth/login", `{"username":"invitee","password":"`+pw+`","code":"000000"}`))
		if rec.Code == 200 {
			t.Errorf("password login of an activated invitee succeeded before enrollment")
		}
	}
	// With requireTotp on, a pending user cannot sign in by that method before enrolling.
	o2 := qaNewOIDC(t, true)
	if _, err := o2.auth.Invite(context.Background(), auth.InviteRequest{Username: "invitee", Role: "member", Email: "invitee@example.com"}); err != nil {
		t.Fatal(err)
	}
	rec = o2.signIn("inv-sub", "invitee@example.com", nil)
	if qaSessionCookie(rec) != "" {
		t.Errorf("a pending user signed in by OIDC with requireTotp on")
	}
	var step bool
	for _, c := range rec.Result().Cookies() {
		if c.Path == "/api/v1/auth/totp" && c.Value != "" {
			step = true
		}
	}
	if step {
		t.Errorf("a pending user was offered a code step with requireTotp on, before enrolling")
	}
	if n := qaCount(t, o2.harness, `SELECT count(*) FROM core_users WHERE username = 'invitee' AND state = 'active'`); n != 0 {
		t.Errorf("the pending user was activated with requireTotp on")
	}
}

func TestA60_disabled_user_cannot_use_oidc(t *testing.T) {
	o := qaNewOIDC(t, false)
	if _, err := o.auth.SetDisabled(context.Background(), "admin", true); err != nil {
		// The last active admin cannot be disabled: add another admin first.
		if _, _, err := o.auth.CreateUserWithRole(context.Background(), "second", testPassword, "admin"); err != nil {
			t.Fatal(err)
		}
		qaExec(t, o.harness, `UPDATE core_users SET state = 'active' WHERE username = 'second'`)
		if _, err := o.auth.SetDisabled(context.Background(), "admin", true); err != nil {
			t.Fatal(err)
		}
	}
	rec := o.signIn("sub-1", "admin@example.com", nil)
	if qaSessionCookie(rec) != "" || qaCount(t, o.harness, `SELECT count(*) FROM core_sessions`) != 0 {
		t.Errorf("a disabled user signed in by OIDC")
	}
}

// ---- Tailscale ----------------------------------------------------------------

type qaTS struct {
	*harness
	whois  auth.TailnetWhois
	calls  atomic.Int32
	failed bool
}

const (
	qaTSController = "100.64.0.1"
	qaTSNode       = "100.64.0.9"
)

func qaNewTS(t *testing.T) *qaTS {
	t.Helper()
	h := newHarness(t)
	q := &qaTS{harness: h, whois: auth.TailnetWhois{LoginName: "alice@example.com", UserID: 4242, NodeName: "alice-laptop", StableID: "nAlice",
		Addresses: []string{qaTSNode}}}
	h.cfg.Whois = func(_ context.Context, addr netip.Addr) (json.RawMessage, error) {
		q.calls.Add(1)
		if q.failed {
			return nil, errcode.New(errcode.Unavailable, "no agent")
		}
		return json.Marshal(q.whois)
	}
	qaRebuild(t, h)
	h.srv.SetTailscaleAddrs(func() []netip.Addr { return []netip.Addr{netip.MustParseAddr(qaTSController)} })
	if err := listen.SaveTailscaleAddrs(context.Background(), h.db, []netip.Addr{netip.MustParseAddr(qaTSController)}); err != nil {
		t.Fatal(err)
	}
	qaExec(t, h, `INSERT INTO auth_settings (id, tailscale_enabled, updated_at) VALUES (1, 1, '2026-11-24T00:00:00Z')`)
	if _, err := h.auth.SetIdentity(context.Background(), "admin", "", "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	return q
}

// req builds a request from remote on the listener at local; proxied marks it as passed through a proxy.
func (q *qaTS) req(method, local, remote, body string, proxied bool) *http.Request {
	target := "https://" + local + ":8443/api/v1/auth/tailscale"
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.RemoteAddr = remote + ":51000"
	ctx := context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP(local), Port: 8443})
	if proxied {
		ctx = core.WithProxied(ctx, core.Proxied{})
	}
	r = r.WithContext(ctx)
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://"+local+":8443")
	}
	return r
}

func TestA16_tailscale_continue_as(t *testing.T) {
	q := qaNewTS(t)
	rec := q.serve(q.req("GET", qaTSController, qaTSNode, "", false))
	var match struct{ Username, LoginName, NodeName string }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &match) != nil || match.Username != "admin" || match.LoginName != "alice@example.com" || match.NodeName != "alice-laptop" {
		t.Fatalf("GET tailscale = %d %s, want a match of admin by alice@example.com from alice-laptop", rec.Code, rec.Body)
	}
	rec = q.serve(q.req("POST", qaTSController, qaTSNode, "{}", false))
	var out struct {
		User struct{ Username string } `json:"user"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.User.Username != "admin" {
		t.Fatalf("Continue as = %d %s, want admin signed in", rec.Code, rec.Body)
	}
	var cookie string
	for _, c := range rec.Result().Cookies() {
		if c.Value != "" && strings.Contains(c.Name, "session") {
			cookie = c.Value
		}
	}
	if cookie == "" || qaCount(t, q.harness, `SELECT count(*) FROM core_sessions`) != 1 {
		t.Errorf("Continue as set no session")
	}
	var pinned int64
	_ = q.db.Read().QueryRow(`SELECT coalesce(tailnet_user_id, '') FROM core_users WHERE username = 'admin'`).Scan(&pinned)
	if pinned != 4242 {
		t.Errorf("the whois user id was not pinned on the first sign-in: %d", pinned)
	}
	// A POST without the request's own Origin is refused.
	r := q.req("POST", qaTSController, qaTSNode, "{}", false)
	r.Header.Set("Origin", "https://evil.example")
	if rec := q.serve(r); rec.Code == 200 {
		t.Errorf("Continue as with a foreign Origin succeeded")
	}
}

func TestA17_tailscale_refusals(t *testing.T) {
	check := func(name string, q *qaTS, r *http.Request, wantCalls int32) {
		t.Helper()
		_ = wantCalls
		before := qaCount(t, q.harness, `SELECT count(*) FROM core_sessions`)
		rec := q.serve(r)
		if rec.Code == 200 && strings.Contains(rec.Body.String(), `"username"`) && r.Method == "POST" {
			t.Errorf("%s: signed in: %s", name, rec.Body)
		}
		if n := qaCount(t, q.harness, `SELECT count(*) FROM core_sessions`); n != before {
			t.Errorf("%s: created a session", name)
		}
		if wantCalls >= 0 && q.calls.Load() != wantCalls {
			t.Errorf("%s: %d agent calls, want %d", name, q.calls.Load(), wantCalls)
		}
	}
	for _, method := range []string{"GET", "POST"} {
		body := ""
		if method == "POST" {
			body = "{}"
		}
		q := qaNewTS(t)
		check(method+" on another listener", q, q.req(method, "192.0.2.50", qaTSNode, body, false), 0)
		check(method+" on loopback", q, q.req(method, "127.0.0.1", qaTSNode, body, false), 0)
		check(method+" proxied", q, q.req(method, qaTSController, qaTSNode, body, true), 0)
		q = qaNewTS(t)
		qaExec(t, q.harness, `UPDATE auth_settings SET tailscale_enabled = 0`)
		check(method+" with the method off", q, q.req(method, qaTSController, qaTSNode, body, false), 0)
		q = qaNewTS(t)
		q.whois.Tags = []string{"tag:server"}
		check(method+" from a tagged node", q, q.req(method, qaTSController, qaTSNode, body, false), -1)
		if q.calls.Load() == 0 {
			t.Errorf("%s from a tagged node: the whois was never asked, so the tag cannot have been the reason", method)
		}
	}
}

func TestA54_tailscale_refused_from_darkpanel_hosts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(q *qaTS)
	}{
		{"an enrolled host's address", func(q *qaTS) {
			qaHosts(t, q.harness, "managed")
			qaExec(t, q.harness, `UPDATE core_hosts SET state = 'enrolled' WHERE id = 'managed'`)
			qaExec(t, q.harness, `INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES ('managed', ?, 'nManaged', '2026-11-24T00:00:00Z')`, qaTSNode)
		}},
		{"an enrolled host that is offline with its module disabled", func(q *qaTS) {
			qaHosts(t, q.harness, "managed")
			qaExec(t, q.harness, `UPDATE core_hosts SET state = 'enrolled' WHERE id = 'managed'`)
			qaExec(t, q.harness, `INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES ('managed', ?, 'nManaged', '2026-11-24T00:00:00Z')`, qaTSNode)
			qaExec(t, q.harness, `INSERT INTO core_module_state (host_id, module_id, enabled, available, checked_at) VALUES ('managed', 'tailscale', 0, 0, '2026-11-24T00:00:00Z')`)
		}},
		{"a node whose whois address is a host's", func(q *qaTS) {
			qaHosts(t, q.harness, "managed")
			qaExec(t, q.harness, `INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES ('managed', '100.64.0.77', 'nManaged', '2026-11-24T00:00:00Z')`)
			q.whois.Addresses = []string{qaTSNode, "100.64.0.77"}
		}},
		{"a node with a host's stable id", func(q *qaTS) {
			qaHosts(t, q.harness, "managed")
			qaExec(t, q.harness, `INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES ('managed', '100.64.0.88', 'nAlice', '2026-11-24T00:00:00Z')`)
		}},
		{"the controller's own address", func(q *qaTS) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := qaNewTS(t)
			tc.setup(q)
			remote := qaTSNode
			if tc.name == "the controller's own address" {
				remote = qaTSController
				q.whois.Addresses = []string{qaTSController}
			}
			for _, method := range []string{"GET", "POST"} {
				body := ""
				if method == "POST" {
					body = "{}"
				}
				rec := q.serve(q.req(method, qaTSController, remote, body, false))
				if rec.Code == 200 {
					t.Errorf("%s from %s: 200 %s, want a refusal", method, tc.name, rec.Body)
				}
			}
			if n := qaCount(t, q.harness, `SELECT count(*) FROM core_sessions`); n != 0 {
				t.Errorf("a session was created for %s", tc.name)
			}
			// A failed lookup refuses too.
			q.failed = true
			if rec := q.serve(q.req("POST", qaTSController, "100.64.0.55", "{}", false)); rec.Code == 200 {
				t.Errorf("a failed whois signed in")
			}
		})
	}
}

func TestA66_first_tailscale_sign_in_activates_an_invite(t *testing.T) {
	q := qaNewTS(t)
	if _, err := q.auth.Invite(context.Background(), auth.InviteRequest{Username: "tsinvite", Role: "member", TailnetLogin: "bob@example.com"}); err != nil {
		t.Fatal(err)
	}
	q.whois = auth.TailnetWhois{LoginName: "bob@example.com", UserID: 77, NodeName: "bob-pc", StableID: "nBob", Addresses: []string{qaTSNode}}
	rec := q.serve(q.req("POST", qaTSController, qaTSNode, "{}", false))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"tsinvite"`) {
		t.Fatalf("an invited user's first Tailscale sign-in = %d %s, want it signed in", rec.Code, rec.Body)
	}
	if n := qaCount(t, q.harness, `SELECT count(*) FROM core_users WHERE username = 'tsinvite' AND state = 'active'`); n != 1 {
		t.Errorf("the invited user is not active after its first Tailscale sign-in")
	}
	if rec := q.serve(request("POST", "/api/v1/auth/login", `{"username":"tsinvite","password":"`+testPassword+`","code":"000000"}`)); rec.Code == 200 {
		t.Errorf("password login of the invitee succeeded before enrollment")
	}
	// requireTotp on: a pending user cannot sign in by this method until it enrolls.
	q2 := qaNewTS(t)
	qaExec(t, q2.harness, `UPDATE auth_settings SET tailscale_require_totp = 1`)
	if _, err := q2.auth.Invite(context.Background(), auth.InviteRequest{Username: "tsinvite", Role: "member", TailnetLogin: "bob@example.com"}); err != nil {
		t.Fatal(err)
	}
	q2.whois = auth.TailnetWhois{LoginName: "bob@example.com", UserID: 77, NodeName: "bob-pc", StableID: "nBob", Addresses: []string{qaTSNode}}
	rec = q2.serve(q2.req("POST", qaTSController, qaTSNode, "{}", false))
	if qaCount(t, q2.harness, `SELECT count(*) FROM core_sessions`) != 0 {
		t.Errorf("a pending user got a session through Tailscale with requireTotp on")
	}
	if n := qaCount(t, q2.harness, `SELECT count(*) FROM core_users WHERE username = 'tsinvite' AND state = 'active'`); n != 0 {
		t.Errorf("the pending user was activated through Tailscale with requireTotp on")
	}
	_ = rec
}

func TestA60_disabled_user_cannot_use_tailscale(t *testing.T) {
	q := qaNewTS(t)
	if _, _, err := q.auth.CreateUserWithRole(context.Background(), "second", testPassword, "admin"); err != nil {
		t.Fatal(err)
	}
	qaExec(t, q.harness, `UPDATE core_users SET state = 'active' WHERE username = 'second'`)
	if _, err := q.auth.SetDisabled(context.Background(), "admin", true); err != nil {
		t.Fatal(err)
	}
	rec := q.serve(q.req("POST", qaTSController, qaTSNode, "{}", false))
	if rec.Code == 200 || qaCount(t, q.harness, `SELECT count(*) FROM core_sessions`) != 0 {
		t.Errorf("a disabled user signed in through Tailscale: %d %s", rec.Code, rec.Body)
	}
}

func TestA18_sign_ins_are_audited_with_method_and_source(t *testing.T) {
	type entry struct {
		method string
		source string
		result string
	}
	read := func(h *harness) (out []entry) {
		entries, _, err := h.audit.List(context.Background(), core.AuditQuery{Limit: 500})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Action != "core.auth.login" {
				continue
			}
			var in struct{ Method string }
			_ = json.Unmarshal(e.Input, &in)
			out = append(out, entry{in.Method, e.Source, e.Result})
		}
		return
	}
	has := func(es []entry, method, result string) bool {
		for _, e := range es {
			if e.method == method && e.result == result && e.source != "" {
				return true
			}
		}
		return false
	}
	// OIDC, success and refusal.
	o := qaNewOIDC(t, false)
	o.signIn("s1", "admin@example.com", nil)
	o.signIn("s2", "nobody@example.com", nil)
	es := read(o.harness)
	if !has(es, "oidc", "ok") || !has(es, "oidc", "error") {
		t.Errorf("OIDC outcomes audited as %+v, want an ok and an error entry with method oidc and a source", es)
	}
	// Tailscale, success and refusal.
	q := qaNewTS(t)
	q.serve(q.req("POST", qaTSController, qaTSNode, "{}", false))
	q.whois.Tags = []string{"tag:x"}
	q.serve(q.req("POST", qaTSController, "100.64.0.20", "{}", false))
	es = read(q.harness)
	if !has(es, "tailscale", "ok") || !has(es, "tailscale", "error") {
		t.Errorf("Tailscale outcomes audited as %+v, want an ok and an error entry with method tailscale and a source", es)
	}
	// Password, success and refusal.
	h := newHarness(t)
	h.serve(request("POST", "/api/v1/auth/login", `{"username":"admin","password":"wrong","code":"000000"}`))
	h.login()
	es = read(h)
	if !has(es, "password", "ok") || !has(es, "password", "error") {
		t.Errorf("password outcomes audited as %+v, want an ok and an error entry with method password and a source", es)
	}
	// Enrollment.
	inv, err := h.auth.Invite(context.Background(), auth.InviteRequest{Username: "newbie", Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	rec := h.serve(request("POST", "/api/v1/enroll/begin", `{"token":"`+inv.Token+`"}`))
	var begin struct{ Secret string }
	_ = json.Unmarshal(rec.Body.Bytes(), &begin)
	code, _ := auth.TOTPCode(begin.Secret, h.clock.Now())
	h.serve(request("POST", "/api/v1/enroll/finish", `{"token":"`+inv.Token+`","password":"a brand new password","code":"`+code+`"}`))
	inv2, err := h.auth.Invite(context.Background(), auth.InviteRequest{Username: "newbie2", Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	h.serve(request("POST", "/api/v1/enroll/begin", `{"token":"`+inv2.Token+`"}`))
	h.serve(request("POST", "/api/v1/enroll/finish", `{"token":"`+inv2.Token+`","password":"a brand new password","code":"000000"}`))
	es = read(h)
	if !has(es, "enrollment", "ok") || !has(es, "enrollment", "error") {
		t.Errorf("enrollment outcomes audited as %+v, want an ok and an error entry with method enrollment and a source", es)
	}
	_ = regexp.MustCompile
}

// qaBus wraps an events bus with a helper that publishes host events.
type qaBus struct{ bus *events.Bus }

func newQABus() *qaBus { return &qaBus{bus: events.NewBus(nil)} }

func (b *qaBus) host(id string) {
	b.bus.Publish(events.TypeHost, events.HostEvent{HostID: id, Online: true})
}

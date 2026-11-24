package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/auth/oidcfake"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/registry"
)

// Acceptance tests of the remembered tailnet devices spec, written from its
// Acceptance section through the HTTP surface: Continue as, the code step and
// the core actions, over a real auth service and the real core actions.

const (
	qaaNodeB = "100.64.0.10"
	qaaNodeC = "100.64.0.11"
)

type qaaEnv struct {
	*qaTS
	idp      *oidcfake.Provider
	adminTok string
}

// qaaNewEnv is a server with the admin linked to alice@example.com (node
// alice-laptop, stable id nAlice, address qaTSNode), Tailscale and OIDC on,
// and the real core actions behind a real dispatcher.
func qaaNewEnv(t *testing.T) *qaaEnv {
	t.Helper()
	ctx := context.Background()
	h := newHarness(t)
	idp := oidcfake.New(t)
	h.auth = auth.New(h.db, coretest.Sealer{}, h.clock, h.audit, auth.Options{Params: testParams, OIDCTransport: idp.Transport()})
	h.cfg.Auth = h.auth

	reg, err := registry.BuildWith(coreactions.Module(coreactions.Deps{Auth: h.auth, Audit: h.audit,
		Modules: modules.All(), Extra: registry.CoreActions(nil, ""), OwnedPaths: install.CoreOwnedPaths()}), nil)
	if err != nil {
		t.Fatal(err)
	}
	discard := slog.New(slog.DiscardHandler)
	agents := dispatch.NewHosts(dispatch.HostsConfig{DB: h.db, Registry: reg, Clients: dispatch.NewStaticClients(), Clock: h.clock, Log: discard})
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: &coretest.MemJournal{},
		Redact: schema.Redact,
		Base:   core.Env{DB: h.db, Sealer: coretest.Sealer{}, Clock: h.clock, Log: discard, Agents: agents},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	disp, err := dispatch.New(dispatch.Config{Registry: reg, Engine: eng, Hosts: agents, DB: h.db, Clock: h.clock, Log: discard})
	if err != nil {
		t.Fatal(err)
	}
	h.cfg.Dispatcher, h.cfg.Registry = disp, reg

	q := &qaTS{harness: h, whois: auth.TailnetWhois{LoginName: "alice@example.com", UserID: 4242, NodeName: "alice-laptop",
		StableID: "nAlice", Addresses: []string{qaTSNode}}}
	h.cfg.Whois = func(context.Context, netip.Addr) (json.RawMessage, error) {
		q.calls.Add(1)
		return json.Marshal(q.whois)
	}
	qaRebuild(t, h)
	h.srv.SetTailscaleAddrs(func() []netip.Addr { return []netip.Addr{netip.MustParseAddr(qaTSController)} })
	if err := listen.SaveTailscaleAddrs(ctx, h.db, []netip.Addr{netip.MustParseAddr(qaTSController)}); err != nil {
		t.Fatal(err)
	}
	ct, err := coretest.Sealer{}.Seal("auth_settings.oidc_client_secret_ct.1", []byte("qa-oidc-secret"))
	if err != nil {
		t.Fatal(err)
	}
	qaExec(t, h, `INSERT INTO auth_settings (id, tailscale_enabled, oidc_issuer, oidc_client_id, oidc_client_secret_ct, oidc_display_name,
		oidc_redirect_base, oidc_require_totp, updated_at) VALUES (1, 1, ?, 'qa-client', ?, 'QA SSO', ?, 0, '2026-11-24T00:00:00Z')`, idp.URL(), ct, qaBase)
	if _, err := h.auth.SetIdentity(ctx, "admin", "admin@example.com", "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	setUID(t, 1000)
	e := &qaaEnv{qaTS: q, idp: idp}
	e.adminTok = h.token(core.ScopeAdmin)
	return e
}

func (e *qaaEnv) adminCred(r *http.Request) *http.Request { return withToken(r, e.adminTok) }

func (e *qaaEnv) socket(r *http.Request) *httptest.ResponseRecorder { return e.socketRequest(r, 0) }

func (e *qaaEnv) asNode(login string, uid int64, name, stable string, addrs ...string) {
	e.whois = auth.TailnetWhois{LoginName: login, UserID: uid, NodeName: name, StableID: stable, Addresses: addrs}
}

func (e *qaaEnv) post(remote string) *httptest.ResponseRecorder {
	return e.serve(e.req("POST", qaTSController, remote, "{}", false))
}

func (e *qaaEnv) get(remote string) *httptest.ResponseRecorder {
	return e.serve(e.req("GET", qaTSController, remote, "", false))
}

func (e *qaaEnv) step(cookie, code, remote string) *httptest.ResponseRecorder {
	return e.qaCodeStep(cookie, code, qaTSController, remote, false)
}

// codeOf advances the clock one TOTP period and returns secret's code.
func (e *qaaEnv) codeOf(secret string) string {
	e.t.Helper()
	e.clock.Advance(auth.TOTPPeriod)
	code, err := auth.TOTPCode(secret, e.clock.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	return code
}

func (e *qaaEnv) sessions() int { return qaCount(e.t, e.harness, `SELECT count(*) FROM core_sessions`) }

func (e *qaaEnv) requireTOTP(on bool) {
	v := 0
	if on {
		v = 1
	}
	qaExec(e.t, e.harness, `UPDATE auth_settings SET tailscale_require_totp = ?`, v)
}

func qaaJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("not JSON: %v: %s", err, rec.Body)
	}
}

// asksCode reports a 200 answer {totp: true}.
func qaaAsksCode(rec *httptest.ResponseRecorder) bool {
	var out struct {
		TOTP bool `json:"totp"`
	}
	return rec.Code == 200 && json.Unmarshal(rec.Body.Bytes(), &out) == nil && out.TOTP
}

// signedInAs returns the username of a 200 sign-in answer, "" otherwise.
func qaaSignedInAs(rec *httptest.ResponseRecorder) string {
	var out struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		return ""
	}
	return out.User.Username
}

func (e *qaaEnv) wantRefused(what string, rec *httptest.ResponseRecorder) {
	e.t.Helper()
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), `"auth.invalid_credentials"`) {
		e.t.Errorf("%s = %d %s, want 401 auth.invalid_credentials", what, rec.Code, rec.Body)
	}
}

type qaaDevice struct {
	stable, name        string
	remembered, lastUse time.Time
}

func (e *qaaEnv) devices(username string) []qaaDevice {
	e.t.Helper()
	rows, err := e.db.Read().Query(`SELECT d.stable_id, d.node_name, d.remembered_at, d.last_used_at FROM core_user_tailnet_devices d
		JOIN core_users u ON u.id = d.user_id WHERE u.username = ? ORDER BY d.stable_id`, username)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []qaaDevice
	for rows.Next() {
		var d qaaDevice
		var rem, used string
		if err := rows.Scan(&d.stable, &d.name, &rem, &used); err != nil {
			e.t.Fatal(err)
		}
		if d.remembered, err = time.Parse(time.RFC3339Nano, rem); err != nil {
			e.t.Fatal(err)
		}
		if d.lastUse, err = time.Parse(time.RFC3339Nano, used); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

func (e *qaaEnv) deviceIDs(username string) []string {
	var ids []string
	for _, d := range e.devices(username) {
		ids = append(ids, d.stable)
	}
	return ids
}

// seed remembers stable for username as a right code would, without the flow.
func (e *qaaEnv) seed(username, stable, name string) {
	e.t.Helper()
	at := e.clock.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
	qaExec(e.t, e.harness, `INSERT INTO core_user_tailnet_devices (user_id, stable_id, node_name, remembered_at, last_used_at)
		SELECT id, ?, ?, ?, ? FROM core_users WHERE username = ?`, stable, name, at, at, username)
}

type qaaUser struct {
	name, id, secret, login string
}

// member is an active, code-ready member linked to <name>@example.com.
func (e *qaaEnv) member(name string) qaaUser {
	e.t.Helper()
	ctx := context.Background()
	_, secret, err := e.auth.CreateUserWithRole(ctx, name, testPassword, "member")
	if err != nil {
		e.t.Fatal(err)
	}
	u, err := e.auth.ActivateUser(ctx, name, e.codeOf(secret))
	if err != nil {
		e.t.Fatal(err)
	}
	login := name + "@example.com"
	if _, err := e.auth.SetIdentity(ctx, name, "", login); err != nil {
		e.t.Fatal(err)
	}
	return qaaUser{name: name, id: u.ID, secret: secret, login: login}
}

// memberCred is a session of u.
func (e *qaaEnv) memberCred(u qaaUser) func(*http.Request) *http.Request {
	e.t.Helper()
	res, err := e.auth.Login(context.Background(), auth.LoginRequest{Username: u.name, Password: testPassword, Code: e.codeOf(u.secret), Source: "192.0.2.10"})
	if err != nil {
		e.t.Fatal(err)
	}
	return func(r *http.Request) *http.Request { return withSession(r, res.SessionID, res.CSRFToken) }
}

func (e *qaaEnv) memberToken(u qaaUser, scope core.Scope) func(*http.Request) *http.Request {
	e.t.Helper()
	_, tok, err := e.auth.CreateToken(context.Background(), u.id, "qaa", scope, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	return func(r *http.Request) *http.Request { return withToken(r, tok) }
}

// remember goes through Continue as and a right code from u at addr.
func (e *qaaEnv) remember(u qaaUser, uid int64, name, stable, addr string) {
	e.t.Helper()
	e.asNode(u.login, uid, name, stable, addr)
	rec := e.post(addr)
	cookie := qaStepCookie(e.t, rec)
	if !qaaAsksCode(rec) {
		e.t.Fatalf("Continue as of %s from %s = %d %s, want the code step", u.name, addr, rec.Code, rec.Body)
	}
	if rec := e.step(cookie, e.codeOf(u.secret), addr); rec.Code != 200 {
		e.t.Fatalf("the code step of %s from %s = %d %s, want a session", u.name, addr, rec.Code, rec.Body)
	}
}

// oidcInvitee invites name and has its first OIDC sign-in activate it: it is
// active, has typed no code and has no password of its own.
func (e *qaaEnv) oidcInvitee(name string) qaaUser {
	e.t.Helper()
	login, email := name+"@example.com", name+"@example.com"
	if _, err := e.auth.Invite(context.Background(), auth.InviteRequest{Username: name, Role: "member", Email: email, TailnetLogin: login}); err != nil {
		e.t.Fatal(err)
	}
	o := &qaOIDC{harness: e.harness, idp: e.idp}
	if rec := o.signIn("sub-"+name, email, nil); qaSessionCookie(rec) == "" {
		e.t.Fatalf("the OIDC sign-in of invitee %s opened no session: %d %s", name, rec.Code, rec.Body)
	}
	if n := qaCount(e.t, e.harness, `SELECT count(*) FROM core_users WHERE username = ? AND state = 'active'`, name); n != 1 {
		e.t.Fatalf("invitee %s is not active after its OIDC sign-in", name)
	}
	return qaaUser{name: name, login: login}
}

// ---- actions ------------------------------------------------------------------

type qaaCall struct {
	status       int
	code         string
	state        string
	confirmation string
	output       json.RawMessage
}

func qaaProblemCode(rec *httptest.ResponseRecorder) string {
	var p struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	return p.Code
}

type qaaServe func(*http.Request) *httptest.ResponseRecorder

// act plans and applies a controller-side action, typing typed as the phrase
// when it is not empty and the plan's phrase otherwise.
func (e *qaaEnv) act(serve qaaServe, cred func(*http.Request) *http.Request, name string, input any, typed string) qaaCall {
	e.t.Helper()
	body, _ := json.Marshal(map[string]any{"input": input})
	rec := serve(cred(request("POST", "/api/v1/actions/"+name+"/v1/plan", string(body))))
	c := qaaCall{status: rec.Code}
	if rec.Code != 200 {
		c.code = qaaProblemCode(rec)
		return c
	}
	var plan struct {
		PlanID       string  `json:"planId"`
		Confirmation *string `json:"confirmation"`
	}
	qaaJSON(e.t, rec, &plan)
	ab := map[string]any{"planId": plan.PlanID}
	if plan.Confirmation != nil {
		c.confirmation = *plan.Confirmation
		ab["confirmation"] = *plan.Confirmation
	}
	if typed != "" {
		ab["confirmation"] = typed
	}
	b, _ := json.Marshal(ab)
	rec = serve(cred(request("POST", "/api/v1/actions/"+name+"/v1/apply", string(b))))
	c.status = rec.Code
	if rec.Code != 200 {
		c.code = qaaProblemCode(rec)
		return c
	}
	var ch struct {
		State  string          `json:"state"`
		Output json.RawMessage `json:"output"`
		Error  *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	qaaJSON(e.t, rec, &ch)
	c.state, c.output = ch.State, ch.Output
	if ch.Error != nil {
		c.code = ch.Error.Code
	}
	return c
}

func (c qaaCall) ok() bool { return c.status == 200 && c.state == "completed" }

func (e *qaaEnv) mustAct(serve qaaServe, cred func(*http.Request) *http.Request, name string, input any) qaaCall {
	e.t.Helper()
	c := e.act(serve, cred, name, input, "")
	if !c.ok() {
		e.t.Fatalf("%s: status %d, state %q, code %q", name, c.status, c.state, c.code)
	}
	return c
}

func (e *qaaEnv) read(cred func(*http.Request) *http.Request, name string, input any) (status int, out json.RawMessage, code string) {
	e.t.Helper()
	body, _ := json.Marshal(map[string]any{"input": input})
	rec := e.serve(cred(request("POST", "/api/v1/actions/"+name+"/v1/read", string(body))))
	if rec.Code != 200 {
		return rec.Code, nil, qaaProblemCode(rec)
	}
	var r struct {
		Output json.RawMessage `json:"output"`
	}
	qaaJSON(e.t, rec, &r)
	return rec.Code, r.Output, ""
}

type qaaListed struct {
	Devices []struct {
		StableID     string `json:"stableId"`
		NodeName     string `json:"nodeName"`
		RememberedAt string `json:"rememberedAt"`
		LastUsedAt   string `json:"lastUsedAt"`
	} `json:"devices"`
}

func (e *qaaEnv) listIDs(cred func(*http.Request) *http.Request, name string, input any) []string {
	e.t.Helper()
	status, out, code := e.read(cred, name, input)
	if status != 200 {
		e.t.Fatalf("%s = %d %s", name, status, code)
	}
	var l qaaListed
	if err := json.Unmarshal(out, &l); err != nil {
		e.t.Fatalf("%s output %s: %v", name, out, err)
	}
	ids := []string{}
	for _, d := range l.Devices {
		ids = append(ids, d.StableID)
	}
	return ids
}

// ---- audit --------------------------------------------------------------------

type qaaLogin struct {
	result, errCode string
	f               map[string]string
}

// logins are the sign-in audit rows, newest first.
func (e *qaaEnv) logins() []qaaLogin {
	e.t.Helper()
	entries, _, err := e.audit.List(context.Background(), core.AuditQuery{Limit: 1000, Action: auth.AuditLoginAction})
	if err != nil {
		e.t.Fatal(err)
	}
	var out []qaaLogin
	for _, en := range entries {
		l := qaaLogin{result: en.Result, errCode: en.ErrorCode, f: map[string]string{}}
		_ = json.Unmarshal(en.Input, &l.f)
		out = append(out, l)
	}
	return out
}

func (e *qaaEnv) lastLogin() qaaLogin {
	e.t.Helper()
	l := e.logins()
	if len(l) == 0 {
		e.t.Fatal("no sign-in was audited")
	}
	return l[0]
}

// ---- A1 -----------------------------------------------------------------------

func TestQA_A1_FirstContinueAsAsksTheCodeThenRemembers(t *testing.T) {
	e := qaaNewEnv(t)
	rec := e.post(qaTSNode)
	if !qaaAsksCode(rec) {
		t.Fatalf("the first Continue as from an unremembered node = %d %s, want the code step", rec.Code, rec.Body)
	}
	cookie := qaStepCookie(t, rec)
	if n := e.sessions(); n != 0 {
		t.Errorf("%d sessions before the code, want 0", n)
	}
	if ids := e.deviceIDs("admin"); len(ids) != 0 {
		t.Errorf("devices %v before the code, want none", ids)
	}
	rec = e.step(cookie, e.code(), qaTSNode)
	if qaaSignedInAs(rec) != "admin" || e.sessions() != 1 {
		t.Fatalf("a right code = %d %s (%d sessions), want admin signed in", rec.Code, rec.Body, e.sessions())
	}
	if ids := e.deviceIDs("admin"); len(ids) != 1 || ids[0] != "nAlice" {
		t.Errorf("devices %v after the right code, want [nAlice]", ids)
	}
	rec = e.post(qaTSNode)
	if qaaSignedInAs(rec) != "admin" || qaaAsksCode(rec) || e.sessions() != 2 {
		t.Errorf("the next Continue as = %d %s (%d sessions), want a session with no code", rec.Code, rec.Body, e.sessions())
	}
}

// ---- A2, A3, A26 --------------------------------------------------------------

func TestQA_A2_NotCodeReadyUserIsRefusedFromAnUnrememberedNode(t *testing.T) {
	e := qaaNewEnv(t)
	bob := e.oidcInvitee("bob")
	e.asNode(bob.login, 77, "bob-pc", "nBob", qaaNodeB)
	before := e.sessions()
	rec := e.post(qaaNodeB)
	e.wantRefused("Continue as of a user that is not code-ready", rec)
	if n := e.sessions(); n != before {
		t.Errorf("%d sessions, want the %d from before (the OIDC activation's)", n, before)
	}
	if ids := e.deviceIDs("bob"); len(ids) != 0 {
		t.Errorf("devices %v, want none remembered", ids)
	}
	if l := e.lastLogin(); l.result != "error" || l.f["refused"] != "device" || l.f["method"] != "tailscale" {
		t.Errorf("audit row %+v, want a tailscale error with refused: device", l)
	}
}

func TestQA_A3_PendingUserIsRefusedAndStaysPending(t *testing.T) {
	for _, requireTOTP := range []bool{false, true} {
		t.Run(fmt.Sprintf("requireTotp %v", requireTOTP), func(t *testing.T) {
			e := qaaNewEnv(t)
			e.requireTOTP(requireTOTP)
			if _, err := e.auth.Invite(context.Background(), auth.InviteRequest{Username: "pend", Role: "member", TailnetLogin: "pend@example.com"}); err != nil {
				t.Fatal(err)
			}
			e.asNode("pend@example.com", 88, "pend-pc", "nPend", qaaNodeB)
			match := qaFields(t, "GET tailscale of a pending user", e.get(qaaNodeB))
			if match["username"] != "pend" || match["codeReady"] != false || match["remembered"] != false {
				t.Errorf("match = %v, want pend with codeReady false and remembered false", match)
			}
			rec := e.post(qaaNodeB)
			e.wantRefused("Continue as of a pending user", rec)
			if e.sessions() != 0 {
				t.Errorf("a pending user got a session")
			}
			if n := qaCount(t, e.harness, `SELECT count(*) FROM core_users WHERE username = 'pend' AND state = 'pending'`); n != 1 {
				t.Errorf("the invited user is no longer pending")
			}
			if l := e.lastLogin(); l.f["refused"] != "device" {
				t.Errorf("audit row %+v, want refused: device", l)
			}
		})
	}
}

func TestQA_A26_CodeReadyFollowsTheTypedCode(t *testing.T) {
	e := qaaNewEnv(t)
	ctx := context.Background()
	// An OIDC-activated invitee whose password an admin then reset has typed no code.
	bob := e.oidcInvitee("bob")
	e.mustAct(e.socket, func(r *http.Request) *http.Request { return r }, "core.user.reset-password",
		map[string]any{"username": "bob", "password": "a brand new password"})
	e.asNode(bob.login, 77, "bob-pc", "nBob", qaaNodeB)
	if m := qaFields(t, "match", e.get(qaaNodeB)); m["codeReady"] != false {
		t.Errorf("match of the OIDC-activated user whose password was reset = %v, want codeReady false", m)
	}
	e.wantRefused("Continue as of that user", e.post(qaaNodeB))

	// A reset with core.user.reset-totp, activated with a right code, is code-ready.
	mia := e.member("mia")
	out := e.mustAct(e.socket, func(r *http.Request) *http.Request { return r }, "core.user.reset-totp", map[string]any{"username": "mia"})
	var reset struct {
		TOTPSecret string `json:"totpSecret"`
	}
	if err := json.Unmarshal(out.output, &reset); err != nil || reset.TOTPSecret == "" {
		t.Fatalf("core.user.reset-totp output %s: %v", out.output, err)
	}
	e.mustAct(e.socket, func(r *http.Request) *http.Request { return r }, "core.user.activate",
		map[string]any{"username": "mia", "code": e.codeOf(reset.TOTPSecret)})
	if u, err := e.auth.GetUser(ctx, "mia"); err != nil || u.State != "active" {
		t.Fatalf("mia after activation: %+v, %v", u, err)
	}
	mia.secret = reset.TOTPSecret
	e.clock.Advance(time.Minute)
	e.asNode(mia.login, 99, "mia-pc", "nMia", qaaNodeC)
	if m := qaFields(t, "match", e.get(qaaNodeC)); m["codeReady"] != true || m["username"] != "mia" {
		t.Errorf("match of the reset and activated user = %v, want codeReady true", m)
	}
	rec := e.post(qaaNodeC)
	if !qaaAsksCode(rec) {
		t.Fatalf("Continue as of the reset and activated user = %d %s, want the code step", rec.Code, rec.Body)
	}
	if rec := e.step(qaStepCookie(t, rec), e.codeOf(reset.TOTPSecret), qaaNodeC); qaaSignedInAs(rec) != "mia" {
		t.Errorf("the right code of the new secret = %d %s, want mia signed in", rec.Code, rec.Body)
	}
}

// ---- A4, A5 -------------------------------------------------------------------

func TestQA_A4_WrongCodeExpiredTicketAndBackRememberNothing(t *testing.T) {
	e := qaaNewEnv(t)
	check := func(what string) {
		t.Helper()
		if ids := e.deviceIDs("admin"); len(ids) != 0 {
			t.Errorf("%s: devices %v, want none", what, ids)
		}
		rec := e.post(qaTSNode)
		if !qaaAsksCode(rec) {
			t.Errorf("%s: the next Continue as = %d %s, want the code step", what, rec.Code, rec.Body)
		}
	}
	// A wrong code.
	rec := e.post(qaTSNode)
	cookie := qaStepCookie(t, rec)
	e.wantRefused("a wrong code", e.step(cookie, e.wrongCode(), qaTSNode))
	check("after a wrong code")
	// An expired ticket, with a right code.
	cookie = qaStepCookie(t, e.post(qaTSNode))
	e.clock.Advance(auth.SignInTicketTTL + time.Minute)
	e.wantRefused("an expired ticket", e.step(cookie, e.code(), qaTSNode))
	check("after an expired ticket")
	// Back: the page drops the ticket and never posts a code.
	_ = qaStepCookie(t, e.post(qaTSNode))
	check("after Back")
	if e.sessions() != 0 {
		t.Errorf("%d sessions, want none", e.sessions())
	}
}

func TestQA_A5_RememberIsPerUserAndNode(t *testing.T) {
	e := qaaNewEnv(t)
	bob := e.member("bob")
	e.remember(qaaUser{name: "admin", login: "alice@example.com", secret: e.secret}, 4242, "shared-pc", "nShared", qaaNodeB)
	// The same node, now signed in by bob's tailnet login.
	e.asNode(bob.login, 77, "shared-pc", "nShared", qaaNodeB)
	rec := e.post(qaaNodeB)
	if !qaaAsksCode(rec) || qaaSignedInAs(rec) != "" {
		t.Fatalf("Continue as of bob from admin's remembered node = %d %s, want the code step", rec.Code, rec.Body)
	}
	if ids := e.deviceIDs("bob"); len(ids) != 0 {
		t.Errorf("bob has devices %v before his own code", ids)
	}
	if rec := e.step(qaStepCookie(t, rec), e.codeOf(bob.secret), qaaNodeB); qaaSignedInAs(rec) != "bob" {
		t.Fatalf("bob's right code = %d %s", rec.Code, rec.Body)
	}
	if a, b := e.deviceIDs("admin"), e.deviceIDs("bob"); len(a) != 1 || len(b) != 1 {
		t.Errorf("devices admin %v bob %v, want one each", a, b)
	}
}

// ---- A6 -----------------------------------------------------------------------

func (e *qaaEnv) recordHost(id, addr string) {
	e.t.Helper()
	qaHosts(e.t, e.harness, id)
	qaExec(e.t, e.harness, `UPDATE core_hosts SET state = 'enrolled' WHERE id = ?`, id)
	qaExec(e.t, e.harness, `INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES (?, ?, '', '2026-11-24T00:00:00Z')`, id, addr)
}

func TestQA_A6_RememberedNodeIsRefusedOnceAHostIsRecordedFromIt(t *testing.T) {
	e := qaaNewEnv(t)
	admin := qaaUser{name: "admin", login: "alice@example.com", secret: e.secret}
	e.remember(admin, 4242, "alice-laptop", "nAlice", qaTSNode)
	before := e.sessions()
	// A ticket issued while the node is not yet recorded: requireTotp asks the code of a remembered node.
	e.requireTOTP(true)
	rec := e.post(qaTSNode)
	if !qaaAsksCode(rec) {
		t.Fatalf("Continue as with requireTotp on = %d %s, want the code step", rec.Code, rec.Body)
	}
	cookie := qaStepCookie(t, rec)
	e.requireTOTP(false)
	e.recordHost("managed", qaTSNode)

	if rec := e.get(qaTSNode); rec.Code == 200 {
		t.Errorf("the match of a node a host is recorded from = 200 %s, want a refusal", rec.Body)
	}
	e.wantRefused("Continue as from a recorded host's node", e.post(qaTSNode))
	if l := e.lastLogin(); l.f["refused"] != "host" {
		t.Errorf("sign-in audit %+v, want refused: host", l)
	}
	e.wantRefused("the code step of a ticket issued before the recording", e.step(cookie, e.code(), qaTSNode))
	if l := e.lastLogin(); l.f["refused"] != "host" {
		t.Errorf("code step audit %+v, want refused: host", l)
	}
	if n := e.sessions(); n != before {
		t.Errorf("%d sessions, want the %d from before the recording", n, before)
	}
}

// ---- A10, A11 -----------------------------------------------------------------

func TestQA_A10_RequireTotpAsksEveryTimeAndRemembers(t *testing.T) {
	e := qaaNewEnv(t)
	e.requireTOTP(true)
	rec := e.post(qaTSNode)
	out := qaFields(t, "Continue as with requireTotp on", rec)
	if out["totp"] != true || out["remember"] != false {
		t.Fatalf("Continue as = %v, want totp true and remember false", out)
	}
	if rec := e.step(qaStepCookie(t, rec), e.code(), qaTSNode); qaaSignedInAs(rec) != "admin" {
		t.Fatalf("right code = %d %s", rec.Code, rec.Body)
	}
	ds := e.devices("admin")
	if len(ds) != 1 || ds[0].stable != "nAlice" {
		t.Fatalf("devices %+v after a right code with requireTotp on, want nAlice", ds)
	}
	first := ds[0]
	// Remembered, it still asks every time, and a right code refreshes it.
	e.clock.Advance(time.Hour)
	rec = e.post(qaTSNode)
	if out := qaFields(t, "Continue as of a remembered node", rec); out["totp"] != true || out["remember"] != false {
		t.Fatalf("Continue as of a remembered node = %v, want the code step with remember false", out)
	}
	if rec := e.step(qaStepCookie(t, rec), e.code(), qaTSNode); qaaSignedInAs(rec) != "admin" {
		t.Fatalf("right code = %d %s", rec.Code, rec.Body)
	}
	ds = e.devices("admin")
	if len(ds) != 1 || !ds[0].remembered.Equal(first.remembered) || !ds[0].lastUse.After(first.lastUse) {
		t.Errorf("device %+v after the refresh, want remembered_at %v kept and last use after %v", ds, first.remembered, first.lastUse)
	}
	// Turned off, the remembered node signs in without a code.
	e.requireTOTP(false)
	if rec := e.post(qaTSNode); qaaSignedInAs(rec) != "admin" {
		t.Errorf("Continue as after requireTotp went off = %d %s, want a session with no code", rec.Code, rec.Body)
	}
}

func TestQA_A11_RememberedSignInUpdatesLastUseAndNodeName(t *testing.T) {
	e := qaaNewEnv(t)
	e.remember(qaaUser{name: "admin", login: "alice@example.com", secret: e.secret}, 4242, "alice-laptop", "nAlice", qaTSNode)
	first := e.devices("admin")[0]
	e.clock.Advance(2 * time.Hour)
	e.asNode("alice@example.com", 4242, "alice-renamed", "nAlice", qaTSNode)
	if rec := e.post(qaTSNode); qaaSignedInAs(rec) != "admin" {
		t.Fatalf("remembered Continue as = %d %s", rec.Code, rec.Body)
	}
	ds := e.devices("admin")
	if len(ds) != 1 || ds[0].name != "alice-renamed" || !ds[0].lastUse.Equal(e.clock.Now().UTC()) || !ds[0].remembered.Equal(first.remembered) {
		t.Errorf("device %+v, want node name alice-renamed, last use %v, remembered_at %v", ds, e.clock.Now().UTC(), first.remembered)
	}
}

// ---- A12 ----------------------------------------------------------------------

func TestQA_A12_WhatForgetsDevices(t *testing.T) {
	e := qaaNewEnv(t)
	seeded := func(name string) {
		t.Helper()
		e.seed(name, "n-"+name+"-1", name+"-one")
		e.seed(name, "n-"+name+"-2", name+"-two")
		if got := e.deviceIDs(name); len(got) != 2 {
			t.Fatalf("seeding %s: devices %v", name, got)
		}
	}
	none := func(what, name string) {
		t.Helper()
		if got := e.deviceIDs(name); len(got) != 0 {
			t.Errorf("%s: %s still has devices %v", what, name, got)
		}
	}
	noCred := func(r *http.Request) *http.Request { return r }

	e.member("rst")
	seeded("rst")
	e.mustAct(e.socket, noCred, "core.user.reset-totp", map[string]any{"username": "rst"})
	none("core.user.reset-totp", "rst")

	if _, err := e.auth.Invite(context.Background(), auth.InviteRequest{Username: "enr", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	inv, err := e.auth.RenewInvite(context.Background(), "enr")
	if err != nil {
		t.Fatal(err)
	}
	seeded("enr")
	if rec := e.serve(request("POST", "/api/v1/enroll/begin", `{"token":"`+inv.Token+`"}`)); rec.Code != 200 {
		t.Fatalf("enroll/begin = %d %s", rec.Code, rec.Body)
	}
	none("starting an enrollment", "enr")

	e.member("lnk")
	seeded("lnk")
	e.mustAct(e.adminServe, e.adminCred, "core.user.set-identity", map[string]any{"username": "lnk", "email": "", "tailnetLogin": "lnk-other@example.com"})
	none("changing the tailnet login", "lnk")

	e.member("del")
	seeded("del")
	e.mustAct(e.socket, noCred, "core.user.delete", map[string]any{"username": "del"})
	if n := qaCount(t, e.harness, `SELECT count(*) FROM core_user_tailnet_devices WHERE stable_id LIKE 'n-del-%'`); n != 0 {
		t.Errorf("deleting the user left %d device rows", n)
	}

	e.member("dis")
	seeded("dis")
	e.mustAct(e.adminServe, e.adminCred, "core.user.disable", map[string]any{"username": "dis"})
	e.mustAct(e.adminServe, e.adminCred, "core.user.enable", map[string]any{"username": "dis"})
	if got := e.deviceIDs("dis"); len(got) != 2 {
		t.Errorf("after disable and enable the devices are %v, want both kept", got)
	}
}

func (e *qaaEnv) adminServe(r *http.Request) *httptest.ResponseRecorder { return e.serve(r) }

// ---- A13, A14, A15, A17 -------------------------------------------------------

func TestQA_A13_OwnListAndForget(t *testing.T) {
	e := qaaNewEnv(t)
	admin := qaaUser{name: "admin", login: "alice@example.com", secret: e.secret}
	mia := e.member("mia")
	e.remember(admin, 4242, "alice-laptop", "nAlice", qaTSNode)
	e.clock.Advance(time.Hour)
	e.remember(admin, 4242, "", "nNameless", qaaNodeB)
	e.clock.Advance(time.Hour)
	e.remember(mia, 99, "mia-pc", "nMia", qaaNodeC)

	got := e.listIDs(e.adminCred, "core.tailnetdevice.list", map[string]any{})
	if len(got) != 2 || got[0] != "nNameless" || got[1] != "nAlice" {
		t.Errorf("admin's list %v, want [nNameless nAlice] (newest use first, no other user's device)", got)
	}
	if got := e.listIDs(e.memberCred(mia), "core.tailnetdevice.list", map[string]any{}); len(got) != 1 || got[0] != "nMia" {
		t.Errorf("mia's list %v, want [nMia]", got)
	}
	// Using the older device makes it the newest.
	e.clock.Advance(time.Hour)
	e.asNode(admin.login, 4242, "alice-laptop", "nAlice", qaTSNode)
	if rec := e.post(qaTSNode); qaaSignedInAs(rec) != "admin" {
		t.Fatalf("remembered Continue as = %d %s", rec.Code, rec.Body)
	}
	if got := e.listIDs(e.adminCred, "core.tailnetdevice.list", map[string]any{}); len(got) != 2 || got[0] != "nAlice" {
		t.Errorf("admin's list %v after using nAlice, want nAlice first", got)
	}

	// The phrase is the stable id, also for a device with no node name.
	c := e.act(e.adminServe, e.adminCred, "core.tailnetdevice.forget", map[string]any{"stableId": "nNameless"}, "wrong phrase")
	if c.confirmation != "nNameless" {
		t.Errorf("the plan's phrase is %q, want the stable id nNameless", c.confirmation)
	}
	if c.ok() {
		t.Errorf("forget applied with a wrong typed phrase")
	}
	if ids := e.deviceIDs("admin"); len(ids) != 2 {
		t.Fatalf("devices %v after a refused forget, want both", ids)
	}
	e.mustAct(e.adminServe, e.adminCred, "core.tailnetdevice.forget", map[string]any{"stableId": "nNameless"})
	if ids := e.deviceIDs("admin"); len(ids) != 1 || ids[0] != "nAlice" {
		t.Errorf("devices %v after the forget, want [nAlice]", ids)
	}
	e.asNode(admin.login, 4242, "", "nNameless", qaaNodeB)
	if rec := e.post(qaaNodeB); !qaaAsksCode(rec) {
		t.Errorf("Continue as from the forgotten node = %d %s, want the code step", rec.Code, rec.Body)
	}
}

func TestQA_A14_ForgetCannotReachAnotherUsersDeviceOrBeCalledByTokens(t *testing.T) {
	e := qaaNewEnv(t)
	mia := e.member("mia")
	e.seed("admin", "nAdminDev", "admin-pc")
	e.seed("mia", "nMiaDev", "mia-pc")
	cred := e.memberCred(mia)

	c := e.act(e.adminServe, cred, "core.tailnetdevice.forget", map[string]any{"stableId": "nAdminDev"}, "")
	if c.ok() || c.code != string(errcode.NotFound) {
		t.Errorf("a member forgetting the admin's device: status %d state %q code %q, want not_found", c.status, c.state, c.code)
	}
	if ids := e.deviceIDs("admin"); len(ids) != 1 {
		t.Errorf("the admin's devices %v after a member's forget, want its device kept", ids)
	}
	for name, tc := range map[string]func(*http.Request) *http.Request{
		"read token":    e.memberToken(mia, core.ScopeRead),
		"operate token": e.memberToken(mia, core.ScopeOperate),
	} {
		c := e.act(e.adminServe, tc, "core.tailnetdevice.forget", map[string]any{"stableId": "nMiaDev"}, "")
		if c.ok() || c.status != 403 {
			t.Errorf("a member's %s forgetting its own device: status %d state %q, want refused 403", name, c.status, c.state)
		}
		if st, _, _ := e.read(tc, "core.user.list-tailnet-devices", map[string]any{"username": "admin"}); st != 403 {
			t.Errorf("a member's %s listing a user's devices = %d, want 403", name, st)
		}
		c = e.act(e.adminServe, tc, "core.user.forget-tailnet-device", map[string]any{"username": "admin", "stableId": "nAdminDev"}, "")
		if c.ok() || c.status != 403 {
			t.Errorf("a member's %s on core.user.forget-tailnet-device: status %d, want 403", name, c.status)
		}
	}
	if st, _, _ := e.read(cred, "core.user.list-tailnet-devices", map[string]any{"username": "admin"}); st != 403 {
		t.Errorf("a member session listing another user's devices = %d, want 403", st)
	}
	c = e.act(e.adminServe, cred, "core.user.forget-tailnet-device", map[string]any{"username": "admin", "stableId": "nAdminDev"}, "")
	if c.ok() || c.status != 403 {
		t.Errorf("a member session on core.user.forget-tailnet-device: status %d state %q, want 403", c.status, c.state)
	}
	if ids := e.deviceIDs("mia"); len(ids) != 1 || e.deviceIDs("admin")[0] != "nAdminDev" {
		t.Errorf("devices changed: mia %v, admin %v", ids, e.deviceIDs("admin"))
	}
	// A member token can read its own list, as it reads its own tokens.
	if got := e.listIDs(e.memberToken(mia, core.ScopeRead), "core.tailnetdevice.list", map[string]any{}); len(got) != 1 || got[0] != "nMiaDev" {
		t.Errorf("a member's read token lists %v, want [nMiaDev]", got)
	}
}

func TestQA_A15_AdminListsAndForgetsAnyUsersDevice(t *testing.T) {
	e := qaaNewEnv(t)
	e.member("mia")
	e.seed("mia", "nMia1", "mia-one")
	e.seed("mia", "nMia2", "mia-two")
	got := e.listIDs(e.adminCred, "core.user.list-tailnet-devices", map[string]any{"username": "mia"})
	if len(got) != 2 {
		t.Fatalf("the admin's list of mia's devices %v, want two", got)
	}
	e.mustAct(e.adminServe, e.adminCred, "core.user.forget-tailnet-device", map[string]any{"username": "mia", "stableId": "nMia1"})
	if ids := e.deviceIDs("mia"); len(ids) != 1 || ids[0] != "nMia2" {
		t.Errorf("mia's devices %v after the admin's forget, want [nMia2]", ids)
	}
	if got := e.listIDs(e.adminCred, "core.user.list-tailnet-devices", map[string]any{"username": "mia"}); len(got) != 1 || got[0] != "nMia2" {
		t.Errorf("the admin's list after the forget %v, want [nMia2]", got)
	}
	c := e.act(e.adminServe, e.adminCred, "core.user.forget-tailnet-device", map[string]any{"username": "mia", "stableId": "nMia1"}, "")
	if c.ok() || c.code != string(errcode.NotFound) {
		t.Errorf("forgetting a device that is gone: status %d code %q, want not_found", c.status, c.code)
	}
}

func TestQA_A17_ForgettingEndsNoSession(t *testing.T) {
	e := qaaNewEnv(t)
	e.remember(qaaUser{name: "admin", login: "alice@example.com", secret: e.secret}, 4242, "alice-laptop", "nAlice", qaTSNode)
	rec := e.post(qaTSNode)
	cookie := qaSessionCookie(rec)
	if qaaSignedInAs(rec) != "admin" || cookie == "" {
		t.Fatalf("remembered Continue as = %d %s", rec.Code, rec.Body)
	}
	whoami := func() int {
		r := request("GET", qaBase+"/api/v1/auth/session", "")
		r.Header.Add("Cookie", SessionCookieSecure+"="+cookie)
		return e.serve(r).Code
	}
	if whoami() != 200 {
		t.Fatalf("the session is not valid before the forget")
	}
	before := e.sessions()
	e.mustAct(e.adminServe, e.adminCred, "core.tailnetdevice.forget", map[string]any{"stableId": "nAlice"})
	if ids := e.deviceIDs("admin"); len(ids) != 0 {
		t.Fatalf("devices %v after the forget", ids)
	}
	if n := e.sessions(); n != before {
		t.Errorf("%d sessions after the forget, want the %d from before", n, before)
	}
	if st := whoami(); st != 200 {
		t.Errorf("the session opened by the forgotten device = %d after the forget, want 200", st)
	}
}

// ---- A16 ----------------------------------------------------------------------

func TestQA_A16_AnswersAndAuditFields(t *testing.T) {
	e := qaaNewEnv(t)
	match := qaFields(t, "GET tailscale", e.get(qaTSNode))
	if match["remembered"] != false || match["codeReady"] != true {
		t.Errorf("match = %v, want remembered false and codeReady true", match)
	}
	rec := e.post(qaTSNode)
	out := qaFields(t, "Continue as", rec)
	if out["totp"] != true || out["remember"] != true {
		t.Errorf("Continue as = %v, want totp true and remember true", out)
	}
	if rec := e.step(qaStepCookie(t, rec), e.code(), qaTSNode); rec.Code != 200 {
		t.Fatalf("code step = %d %s", rec.Code, rec.Body)
	}
	if l := e.lastLogin(); l.result != "ok" || l.f["method"] != "tailscale" || l.f["device"] != "new" {
		t.Errorf("the code step's audit row %+v, want ok with device: new", l)
	}
	if rec := e.post(qaTSNode); qaaSignedInAs(rec) != "admin" {
		t.Fatalf("remembered Continue as = %d %s", rec.Code, rec.Body)
	}
	if l := e.lastLogin(); l.result != "ok" || l.f["device"] != "remembered" {
		t.Errorf("the remembered sign-in's audit row %+v, want ok with device: remembered", l)
	}
	e.clock.Advance(time.Minute)
	match = qaFields(t, "GET tailscale after remembering", e.get(qaTSNode))
	if match["remembered"] != true || match["codeReady"] != true {
		t.Errorf("match after remembering = %v, want both true", match)
	}
	// A wrong code carries no refusal cause.
	e.requireTOTP(true)
	rec = e.post(qaTSNode)
	e.wantRefused("a wrong code", e.step(qaStepCookie(t, rec), e.wrongCode(), qaTSNode))
	if l := e.lastLogin(); l.result != "error" || l.f["refused"] != "" {
		t.Errorf("a wrong code's audit row %+v, want an error with no refused field", l)
	}
}

func TestQA_A16_RefusalCauses(t *testing.T) {
	for _, tc := range []struct {
		cause string
		setup func(e *qaaEnv)
	}{
		{"host", func(e *qaaEnv) { e.recordHost("managed", qaTSNode) }},
		{"node", func(e *qaaEnv) { e.whois.Tags = []string{"tag:server"} }},
		{"device", func(e *qaaEnv) {
			bob := e.oidcInvitee("bob")
			e.asNode(bob.login, 77, "bob-pc", "nBob", qaTSNode)
		}},
		{"link", func(e *qaaEnv) { e.asNode("nobody@example.com", 5, "x", "nX", qaTSNode) }},
		{"user", func(e *qaaEnv) {
			mia := e.member("mia")
			if _, err := e.auth.SetDisabled(context.Background(), "mia", true); err != nil {
				e.t.Fatal(err)
			}
			e.asNode(mia.login, 99, "mia-pc", "nMia", qaTSNode)
		}},
	} {
		t.Run(tc.cause, func(t *testing.T) {
			e := qaaNewEnv(t)
			if tc.cause == "host" || tc.cause == "node" {
				// Remembered, so only the cause under test refuses it.
				e.remember(qaaUser{name: "admin", login: "alice@example.com", secret: e.secret}, 4242, "alice-laptop", "nAlice", qaTSNode)
			}
			tc.setup(e)
			before := e.sessions()
			rec := e.post(qaTSNode)
			if tc.cause == "link" {
				if rec.Code == 200 {
					t.Errorf("Continue as of a login no user is linked to = 200 %s", rec.Body)
				}
			} else {
				e.wantRefused("Continue as", rec)
			}
			if e.sessions() != before {
				t.Errorf("a refused Continue as opened a session")
			}
			if l := e.lastLogin(); l.result != "error" || l.f["method"] != "tailscale" || l.f["refused"] != tc.cause {
				t.Errorf("audit row %+v, want a tailscale error with refused: %s", l, tc.cause)
			}
		})
	}
}

// ---- A27, A28, A30 ------------------------------------------------------------

func TestQA_A27_ChangedLinkRefusesTheTicket(t *testing.T) {
	// The tailnet login changed after the ticket was issued.
	e := qaaNewEnv(t)
	cookie := qaStepCookie(t, e.post(qaTSNode))
	if _, err := e.auth.SetIdentity(context.Background(), "admin", "admin@example.com", "moved@example.com"); err != nil {
		t.Fatal(err)
	}
	e.wantRefused("a code step after the tailnet login changed", e.step(cookie, e.code(), qaTSNode))
	if l := e.lastLogin(); l.f["refused"] != "link" {
		t.Errorf("audit row %+v, want refused: link", l)
	}
	if e.sessions() != 0 || len(e.devices("admin")) != 0 {
		t.Errorf("sessions %d devices %v, want nothing opened or remembered", e.sessions(), e.deviceIDs("admin"))
	}
	if n := qaCount(t, e.harness, `SELECT count(*) FROM core_users WHERE username = 'admin' AND COALESCE(tailnet_user_id, '') = ''`); n != 1 {
		t.Errorf("the refused code step pinned a tailnet user id")
	}

	// The pinned tailnet user id changed after the ticket was issued.
	e = qaaNewEnv(t)
	cookie = qaStepCookie(t, e.post(qaTSNode))
	qaExec(t, e.harness, `UPDATE core_users SET tailnet_user_id = '9999' WHERE username = 'admin'`)
	e.wantRefused("a code step after the pinned id changed", e.step(cookie, e.code(), qaTSNode))
	if l := e.lastLogin(); l.f["refused"] != "link" {
		t.Errorf("audit row %+v, want refused: link", l)
	}
	if e.sessions() != 0 || len(e.devices("admin")) != 0 {
		t.Errorf("sessions %d devices %v, want nothing opened or remembered", e.sessions(), e.deviceIDs("admin"))
	}
	if n := qaCount(t, e.harness, `SELECT count(*) FROM core_users WHERE username = 'admin' AND tailnet_user_id = '9999'`); n != 1 {
		t.Errorf("the refused code step changed the pinned tailnet user id")
	}
}

func TestQA_A28_RefusalsBeforeTheCodeSpendNoBudget(t *testing.T) {
	const attempts = auth.UsernameFailureLimit + 1
	t.Run("recorded host", func(t *testing.T) {
		e := qaaNewEnv(t)
		cookie := qaStepCookie(t, e.post(qaTSNode))
		e.recordHost("managed", qaTSNode)
		for i := 0; i < attempts; i++ {
			e.wantRefused("a code step for a recorded host", e.step(cookie, e.code(), qaTSNode))
		}
		qaExec(t, e.harness, `DELETE FROM core_host_tailnet`)
		if rec := e.step(cookie, e.code(), qaTSNode); qaaSignedInAs(rec) != "admin" {
			t.Errorf("a right code after %d host refusals = %d %s, want a session", attempts, rec.Code, rec.Body)
		}
	})
	t.Run("changed link", func(t *testing.T) {
		e := qaaNewEnv(t)
		cookie := qaStepCookie(t, e.post(qaTSNode))
		if _, err := e.auth.SetIdentity(context.Background(), "admin", "admin@example.com", "moved@example.com"); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < attempts; i++ {
			e.wantRefused("a code step for a changed link", e.step(cookie, e.code(), qaTSNode))
		}
		if _, err := e.auth.SetIdentity(context.Background(), "admin", "admin@example.com", "alice@example.com"); err != nil {
			t.Fatal(err)
		}
		if rec := e.step(cookie, e.code(), qaTSNode); qaaSignedInAs(rec) != "admin" {
			t.Errorf("a right code after %d link refusals = %d %s, want a session", attempts, rec.Code, rec.Body)
		}
	})
	t.Run("wrong codes", func(t *testing.T) {
		e := qaaNewEnv(t)
		cookie := qaStepCookie(t, e.post(qaTSNode))
		for i := 0; i < auth.UsernameFailureLimit; i++ {
			e.wantRefused("a wrong code", e.step(cookie, e.wrongCode(), qaTSNode))
		}
		rec := e.step(cookie, e.code(), qaTSNode)
		if rec.Code != 429 || !strings.Contains(rec.Body.String(), `"auth.rate_limited"`) {
			t.Errorf("a right code after %d wrong ones = %d %s, want 429 auth.rate_limited", auth.UsernameFailureLimit, rec.Code, rec.Body)
		}
	})
}

func TestQA_A30_CodeStepIsBoundToTheTicketsNode(t *testing.T) {
	e := qaaNewEnv(t)
	cookie := qaStepCookie(t, e.post(qaTSNode))
	type variant struct {
		name          string
		local, remote string
		proxied       bool
	}
	variants := []variant{
		{"another tailnet address", qaTSController, qaaNodeC, false},
		{"the ticket's address on another listener", "192.0.2.50", qaTSNode, false},
		{"the ticket's address through a proxy", qaTSController, qaTSNode, true},
	}
	for round := 0; round < 3; round++ {
		for _, v := range variants {
			var code string
			if round%2 == 1 {
				code = e.wrongCode()
			} else {
				code = e.code()
			}
			rec := e.qaCodeStep(cookie, code, v.local, v.remote, v.proxied)
			e.wantRefused(v.name, rec)
			if l := e.lastLogin(); l.f["refused"] != "node" {
				t.Errorf("%s: audit row %+v, want refused: node", v.name, l)
			}
		}
	}
	if e.sessions() != 0 || len(e.devices("admin")) != 0 {
		t.Errorf("sessions %d devices %v after refused steps, want none", e.sessions(), e.deviceIDs("admin"))
	}
	if n := qaCount(t, e.harness, `SELECT count(*) FROM core_users WHERE username = 'admin' AND COALESCE(tailnet_user_id, '') = ''`); n != 1 {
		t.Errorf("a refused code step pinned a tailnet user id")
	}
	// Nine refusals, more than the budget of five, and the ticket still works from its node.
	rec := e.step(cookie, e.code(), qaTSNode)
	if qaaSignedInAs(rec) != "admin" {
		t.Fatalf("the ticket from its own node = %d %s, want a session", rec.Code, rec.Body)
	}
	if ids := e.deviceIDs("admin"); len(ids) != 1 {
		t.Errorf("devices %v after the right step from its node, want it remembered", ids)
	}
}

// ---- A29 ----------------------------------------------------------------------

func TestQA_A29_ForgetRacingARememberedSignIn(t *testing.T) {
	e := qaaNewEnv(t)
	admin := qaaUser{name: "admin", login: "alice@example.com", secret: e.secret}
	_ = admin
	for i := 0; i < 20; i++ {
		e.clock.Advance(time.Minute + time.Second)
		qaExec(t, e.harness, `DELETE FROM core_user_tailnet_devices`)
		e.seed("admin", "nAlice", "alice-laptop")
		body, _ := json.Marshal(map[string]any{"input": map[string]any{"stableId": "nAlice"}})
		rec := e.serve(e.adminCred(request("POST", "/api/v1/actions/core.tailnetdevice.forget/v1/plan", string(body))))
		var plan struct {
			PlanID       string `json:"planId"`
			Confirmation string `json:"confirmation"`
		}
		if rec.Code != 200 {
			t.Fatalf("plan = %d %s", rec.Code, rec.Body)
		}
		qaaJSON(t, rec, &plan)
		ab, _ := json.Marshal(map[string]any{"planId": plan.PlanID, "confirmation": plan.Confirmation})

		var wg sync.WaitGroup
		results := make([]*httptest.ResponseRecorder, 3)
		var applied *httptest.ResponseRecorder
		wg.Add(1 + len(results))
		start := make(chan struct{})
		go func() {
			defer wg.Done()
			<-start
			applied = e.serve(e.adminCred(request("POST", "/api/v1/actions/core.tailnetdevice.forget/v1/apply", string(ab))))
		}()
		for j := range results {
			go func() {
				defer wg.Done()
				<-start
				results[j] = e.post(qaTSNode)
			}()
		}
		close(start)
		wg.Wait()
		if applied.Code != 200 {
			t.Fatalf("round %d: forget apply = %d %s", i, applied.Code, applied.Body)
		}
		for j, r := range results {
			if qaaSignedInAs(r) != "admin" && !qaaAsksCode(r) {
				t.Errorf("round %d: Continue as %d = %d %s, want a session or the code step", i, j, r.Code, r.Body)
			}
		}
		if ids := e.deviceIDs("admin"); len(ids) != 0 {
			t.Fatalf("round %d: the forgotten device was re-created: %v", i, ids)
		}
	}
}

package api

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestA4_StepUpElevatesForFifteenMinutes(t *testing.T) {
	h := stepUpHarness(t)
	session, csrf := h.login()
	plan := func() *http.Request { return withSession(request("POST", stepUpPlan, `{"input":{}}`), session, csrf) }
	wantProblem(t, "before step-up", h.serve(plan()), http.StatusForbidden, errcode.StepUpRequired)
	rec := h.serve(withSession(request("POST", "/api/v1/auth/step-up", `{"code":"`+h.code()+`"}`), session, csrf))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("step-up with a valid code: %d %s", rec.Code, rec.Body)
	}
	if rec := h.serve(plan()); rec.Code != http.StatusOK {
		t.Fatalf("plan inside the window: %d %s", rec.Code, rec.Body)
	}
	h.clock.Advance(14*time.Minute + 30*time.Second)
	if rec := h.serve(plan()); rec.Code != http.StatusOK {
		t.Errorf("plan 14.5 minutes after step-up: %d %s", rec.Code, rec.Body)
	}
	h.clock.Advance(time.Minute)
	wantProblem(t, "after the 15 minute window", h.serve(plan()), http.StatusForbidden, errcode.StepUpRequired)
}

func TestA5_AnAcceptedCodeIsRefusedAgainForStepUp(t *testing.T) {
	h := stepUpHarness(t)
	session, csrf := h.login() // the sign-in code is the step of the clock now
	stepUp := func(code string) *http.Request {
		return withSession(request("POST", "/api/v1/auth/step-up", `{"code":"`+code+`"}`), session, csrf)
	}
	signInCode, err := auth.TOTPCode(h.secret, h.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rec := h.serve(stepUp(signInCode)); rec.Code == http.StatusNoContent {
		t.Error("the code that signed in was accepted for step-up")
	}
	fresh := h.code()
	if rec := h.serve(stepUp(fresh)); rec.Code != http.StatusNoContent {
		t.Fatalf("a fresh code: %d %s", rec.Code, rec.Body)
	}
	rec := h.serve(stepUp(fresh))
	if rec.Code == http.StatusNoContent {
		t.Error("a step-up code was accepted a second time")
	}
	wantProblem(t, "the same step again", rec, http.StatusConflict, errcode.StepUpWait)
}

func TestA6_ATokenCannotStepUp(t *testing.T) {
	h := stepUpHarness(t)
	tok := h.token(core.ScopeAdmin)
	wantProblem(t, "step-up with a token", h.serve(withToken(request("POST", "/api/v1/auth/step-up", `{"code":"`+h.code()+`"}`), tok)),
		http.StatusForbidden, errcode.StepUpUnavailable)
	wantProblem(t, "a step-up action with a token", h.serve(withToken(request("POST", stepUpPlan, `{"input":{}}`), tok)),
		http.StatusForbidden, errcode.StepUpUnavailable)
}

func TestA7_ATerminalAsksForACodeInsideTheWindowAndAWrongCodeOpensNothing(t *testing.T) {
	r := newTermRig(t, false, 0)
	if rec := r.h.serve(withSession(request("POST", "/api/v1/auth/step-up", `{"code":"`+r.code(r.admin)+`"}`), r.admin.session, r.admin.csrf)); rec.Code != http.StatusNoContent {
		t.Fatalf("step-up: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, "a ticket without a code inside the window", r.postTicket(r.admin, `{}`), http.StatusForbidden, errcode.StepUpRequired)
	r.h.clock.Advance(auth.TOTPPeriod)
	wantProblem(t, "a wrong code", r.postTicket(r.admin, `{"code":"`+r.h.wrongCode()+`"}`), http.StatusUnauthorized, errcode.InvalidCredentials)
	if _, _, code := r.dial(r.admin, termHost, "", nil); code == "" {
		t.Error("a terminal opened without a ticket")
	}
	if n := r.opened.Load(); n != 0 {
		t.Errorf("%d agent channels opened after refused codes", n)
	}
}

func TestA10_OriginAndCookieAreRequired(t *testing.T) {
	r := newTermRig(t, false, 0)
	for _, tc := range []struct {
		name, origin    string
		tls, proxy, ses bool
		accepted        bool
	}{
		{"no origin", "", false, false, true, false},
		{"another site", "https://evil.example", false, false, true, false},
		{"no session cookie", "http://example.com", false, false, false, false},
		{"plain http own origin", "http://example.com", false, false, true, true},
		{"tls own origin", "https://example.com", true, false, true, true},
		{"tls with an http origin", "http://example.com", true, false, true, false},
		{"proxied public origin", "https://panel.example", false, true, true, true},
		{"proxied with the listener's own origin", "http://example.com", false, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tk := r.ticket(r.admin)
			req := request(http.MethodGet, "/api/v1/hosts/"+termHost+"/channels/console/terminal?ticket="+tk+"&cols=80&rows=24", "")
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
				if tc.ses {
					req = withSecureSession(req, r.admin.session)
				}
			} else if tc.ses {
				req = withSession(req, r.admin.session, "")
			}
			if tc.proxy {
				req = req.WithContext(core.WithProxied(req.Context(), core.Proxied{ListenerID: "l1", PublicHost: "panel.example"}))
			}
			before := r.opened.Load()
			rec := r.h.serve(req)
			if accepted := r.opened.Load() > before; accepted != tc.accepted {
				t.Errorf("accepted %v (%d %s), want %v", accepted, rec.Code, rec.Body, tc.accepted)
			}
			for len(r.agents) > 0 {
				_ = (<-r.agents).Close()
			}
		})
	}
}

func TestA97_TicketIsSingleUseBoundAndShortLived(t *testing.T) {
	r := newTermRig(t, false, 0)
	tk := r.ticket(r.admin)
	if _, _, code := r.dial(r.admin, termHost, tk, nil); code != "" {
		t.Fatalf("a fresh ticket: %s", code)
	}
	if _, _, code := r.dial(r.admin, termHost, tk, nil); code == "" {
		t.Error("a ticket opened two terminals")
	}
	late := r.ticket(r.admin)
	r.h.clock.Advance(31 * time.Second)
	if _, _, code := r.dial(r.admin, termHost, late, nil); code == "" {
		t.Error("a 31 s old ticket opened a terminal")
	}
	other := r.addUser("bob", auth.RoleAdmin)
	if _, _, code := r.dial(other, termHost, r.ticket(r.admin), nil); code == "" {
		t.Error("another session used the ticket")
	}
	if _, _, code := r.dial(r.admin, "h2", r.ticket(r.admin), nil); code == "" {
		t.Error("another host accepted the ticket")
	}
	code := r.code(r.admin)
	if _, _, got := r.dial(r.admin, termHost, url.QueryEscape(code), nil); got == "" {
		t.Error("a TOTP code in the ticket parameter opened a terminal")
	}
}

func TestA15_ANinthTerminalForOneUserIsRefused(t *testing.T) {
	r := newTermRig(t, false, 0)
	for i := 0; i < 8; i++ {
		ws, agent, code := r.dial(r.admin, termHost, r.ticket(r.admin), nil)
		if code != "" {
			t.Fatalf("terminal %d: %s", i+1, code)
		}
		keep(t, ws)
		go func() { _, _ = io.Copy(io.Discard, agent) }()
	}
	if _, _, code := r.dial(r.admin, termHost, r.ticket(r.admin), nil); code != string(errcode.ConsoleLimit) {
		t.Errorf("a ninth terminal: %q, want %s", code, errcode.ConsoleLimit)
	}
}

func TestA3_AMemberIsRefusedTheTerminalTicket(t *testing.T) {
	r := newTermRig(t, false, 0)
	for _, role := range []string{"viewer", "operator"} {
		name := "m" + role
		member := r.addUser(name, auth.RoleMember)
		if _, err := r.h.auth.SetGrants(t.Context(), name, []auth.Grant{{Target: "*", Role: role}}); err != nil {
			t.Fatal(err)
		}
		wantProblem(t, "a member with the "+role+" role", r.postTicket(member, `{"code":"`+r.code(member)+`"}`), http.StatusForbidden, errcode.Forbidden)
	}
}

type acceptFileOp struct {
	op, method, query, body, ctype string
	needsWindow                    bool
}

var acceptFileOps = []acceptFileOp{
	{op: "list", method: "GET", query: "path=/srv"},
	{op: "stat", method: "GET", query: "path=/srv/a"},
	{op: "read", method: "GET", query: "path=/srv/a", needsWindow: true},
	{op: "download", method: "GET", query: "path=/srv/a", needsWindow: true},
	{op: "write", method: "POST", query: "path=/srv/a&sha256=ab", body: "text\n", ctype: "application/octet-stream", needsWindow: true},
	{op: "upload", method: "POST", query: "path=/srv/b", body: "bytes", ctype: "application/octet-stream", needsWindow: true},
	{op: "mkdir", method: "POST", body: `{"path":"/srv/d"}`, ctype: "application/json", needsWindow: true},
	{op: "rename", method: "POST", body: `{"path":"/srv/d","to":"/srv/e"}`, ctype: "application/json", needsWindow: true},
	{op: "delete", method: "POST", body: `{"path":"/srv/e","confirm":"/srv/e"}`, ctype: "application/json", needsWindow: true},
	{op: "chmod", method: "POST", body: `{"path":"/srv/a","mode":"0600"}`, ctype: "application/json", needsWindow: true},
	{op: "chown", method: "POST", body: `{"path":"/srv/a","uid":0}`, ctype: "application/json", needsWindow: true},
}

func (f acceptFileOp) req(host string) *http.Request {
	target := "/api/v1/hosts/" + host + "/files/" + f.op
	if f.query != "" {
		target += "?" + f.query
	}
	r := httptest.NewRequest(f.method, target, strings.NewReader(f.body))
	r.RemoteAddr = testRemote
	if f.ctype != "" {
		r.Header.Set("Content-Type", f.ctype)
	}
	return r
}

func TestA61_A4_FileListingNeedsNoCodeAndContentAndWritesNeedTheFifteenMinuteWindow(t *testing.T) {
	h := newHarness(t)
	fakeFiles(h)
	session, csrf := h.login()
	serve := func(f acceptFileOp) *httptest.ResponseRecorder {
		return h.serve(withSession(f.req(testHostID), session, csrf))
	}
	for _, f := range acceptFileOps {
		rec := serve(f)
		if f.needsWindow {
			wantProblem(t, f.op+" before step-up", rec, http.StatusForbidden, errcode.StepUpRequired)
		} else if rec.Code != http.StatusOK {
			t.Errorf("%s before step-up: %d %s; listing and stat need no code", f.op, rec.Code, rec.Body)
		}
	}
	stepUp(t, h, h.code(), session, csrf)
	for _, f := range acceptFileOps {
		if rec := serve(f); rec.Code >= 400 {
			t.Errorf("%s inside the window: %d %s", f.op, rec.Code, rec.Body)
		}
	}
	h.clock.Advance(15*time.Minute + time.Second)
	for _, f := range acceptFileOps {
		rec := serve(f)
		if f.needsWindow {
			wantProblem(t, f.op+" after the window", rec, http.StatusForbidden, errcode.StepUpRequired)
		} else if rec.Code != http.StatusOK {
			t.Errorf("%s after the window: %d; listing and stat need no code", f.op, rec.Code)
		}
	}
}

func TestA3_AMemberIsRefusedEveryFileEndpointAndAnUnseenHostIsNotFound(t *testing.T) {
	h := newHarness(t)
	fakeFiles(h)
	qaHosts(t, h, "g1", "x1")
	qaGroupOf(t, h, "G", "g1")
	for _, role := range []string{"viewer", "operator"} {
		as, _ := qaGrantedUser(t, h, "m-"+role, "G="+role)
		for _, f := range acceptFileOps {
			wantProblem(t, role+" "+f.op, h.serve(as(f.req("g1"))), http.StatusForbidden, errcode.Forbidden)
		}
		for _, f := range acceptFileOps {
			wantProblem(t, role+" "+f.op+" on a host outside the grants", h.serve(as(f.req("x1"))), http.StatusNotFound, errcode.NotFound)
		}
	}
}

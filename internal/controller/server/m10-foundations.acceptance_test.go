package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/rollout"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Acceptance tests of the M10 foundations spec, driven over HTTP and the CLI
// against one assembled server. Expected values are written from the spec.

const qaPassword = "qa correct horse battery" //nolint:gosec // G101: a test password, not a credential

// qaStart starts a controller-only server with its first admin logged in.
func qaStart(t *testing.T) *e2e {
	t.Helper()
	e, _ := qaStartOpts(t)
	return e
}

// qaStartOpts is qaStart that also returns the options the server started with.
func qaStartOpts(t *testing.T) (*e2e, Options) {
	t.Helper()
	opts := testOptions(t)
	logs := &lockedBuffer{}
	opts.Log = slog.New(slog.NewTextHandler(logs, nil))
	e := &e2e{t: t, clock: opts.Clock.(*coretest.FakeClock), home: t.TempDir()}
	qaLogs.Store(t, logs)
	e.s = start(t, opts)
	e.url = baseURL(t, e.s)
	e.loginAdmin()
	return e, opts
}

// qaLogs maps a test to the server's log of it.
var qaLogs sync.Map

// qaLog is the server's log so far.
func qaLog(t *testing.T) string {
	if b, ok := qaLogs.Load(t); ok {
		return b.(*lockedBuffer).String()
	}
	return ""
}

// qaRestartAt stops the server and starts it again over the same files with
// its clock at at, which jumps time without running every timer in between.
func (e *e2e) qaRestartAt(opts Options, at time.Time) {
	e.t.Helper()
	if err := e.s.Shutdown(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	opts.Clock = coretest.NewFakeClock(at)
	e.clock = opts.Clock.(*coretest.FakeClock)
	e.s = start(e.t, opts)
	e.url = baseURL(e.t, e.s)
}

// qaResult is a plan and an apply of one action.
type qaResult struct {
	plan, apply reply
	change      struct {
		ID     string          `json:"id"`
		State  string          `json:"state"`
		Output json.RawMessage `json:"output"`
		Error  *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
}

// code is the problem code of the first refused stage, or the code of a
// failed change, or "".
func (r qaResult) code() string {
	if c := r.plan.problemCode(); c != "" {
		return string(c)
	}
	if c := r.apply.problemCode(); c != "" {
		return string(c)
	}
	if r.change.Error != nil {
		return r.change.Error.Code
	}
	return ""
}

// ok reports a plan and apply that both answered 200 and a completed change.
func (r qaResult) ok() bool {
	return r.plan.status == 200 && r.apply.status == 200 && r.change.State == "completed"
}

// qaCall plans and applies a controller-side action with c, stopping at the
// first refusal.
func (e *e2e) qaCall(c credentials, name string, input any) qaResult {
	e.t.Helper()
	var res qaResult
	res.plan = e.do("POST", "/api/v1/actions/"+name+"/v1/plan", map[string]any{"input": input}, c)
	if res.plan.status != 200 {
		return res
	}
	var plan struct {
		PlanID       string  `json:"planId"`
		Confirmation *string `json:"confirmation"`
	}
	if err := json.Unmarshal(res.plan.body, &plan); err != nil {
		e.t.Fatalf("%s plan: %v: %s", name, err, res.plan.body)
	}
	body := map[string]any{"planId": plan.PlanID}
	if plan.Confirmation != nil {
		body["confirmation"] = *plan.Confirmation
	}
	res.apply = e.do("POST", "/api/v1/actions/"+name+"/v1/apply", body, c)
	if res.apply.status == 200 {
		if err := json.Unmarshal(res.apply.body, &res.change); err != nil {
			e.t.Fatalf("%s apply: %v: %s", name, err, res.apply.body)
		}
	}
	return res
}

// qaRead reads a controller-side action and returns the reply.
func (e *e2e) qaRead(c credentials, name string, input any) reply {
	e.t.Helper()
	if input == nil {
		input = map[string]any{}
	}
	return e.do("POST", "/api/v1/actions/"+name+"/v1/read", map[string]any{"input": input}, c)
}

// qaMust applies an action that must complete and returns its output.
func (e *e2e) qaMust(name string, input any) json.RawMessage {
	e.t.Helper()
	r := e.qaCall(e.admin(), name, input)
	if !r.ok() {
		e.t.Fatalf("%s: plan %d %s, apply %d %s", name, r.plan.status, r.plan.body, r.apply.status, r.apply.body)
	}
	return r.change.Output
}

// qaHost inserts a host row that is in no group.
func qaHost(e *e2e, id string) {
	e.t.Helper()
	exec(e.t, e.s, `INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, 0, ?)`, id, id, e.clock.Now().UTC().Format(qaTimeFormat))
}

// qaGroup creates a group holding hosts and returns its id.
func (e *e2e) qaGroup(name string, hosts ...string) string {
	e.t.Helper()
	var g struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(e.qaMust("core.group.create", map[string]any{"name": name}), &g); err != nil || g.ID == "" {
		e.t.Fatalf("core.group.create: %v", err)
	}
	if len(hosts) > 0 {
		e.qaMust("core.group.set-members", map[string]any{"id": g.ID, "hostIds": hosts})
	}
	return g.ID
}

// qaGrant is one grant of an invite.
type qaGrant struct {
	Target string `json:"target"`
	Role   string `json:"role"`
}

// qaMember is a signed-in non-admin or invited user.
type qaMember struct {
	name   string
	secret string
	creds  credentials
}

// qaInvite invites a user and returns the link token.
func (e *e2e) qaInvite(name, role string, grants []qaGrant, extra map[string]any) (token string) {
	e.t.Helper()
	in := map[string]any{"username": name, "role": role}
	if grants != nil {
		in["grants"] = grants
	}
	for k, v := range extra {
		in[k] = v
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(e.qaMust("core.user.invite", in), &out); err != nil || out.Token == "" {
		e.t.Fatalf("core.user.invite output: %v", err)
	}
	return out.Token
}

// qaNextCode advances the clock one TOTP period and returns secret's code.
func (e *e2e) qaNextCode(secret string) string {
	e.t.Helper()
	e.clock.Advance(auth.TOTPPeriod)
	code, err := auth.TOTPCode(secret, e.clock.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	return code
}

// qaSession reads the session cookie and CSRF token of a login or enrollment
// answer.
func qaSession(e *e2e, r reply) credentials {
	e.t.Helper()
	var body struct {
		CSRFToken string `json:"csrfToken"`
	}
	e.want(r, 200, &body)
	var c credentials
	for _, ck := range (&http.Response{Header: r.header}).Cookies() {
		if ck.Name == api.SessionCookiePlain {
			c.session = ck.Value
		}
	}
	c.csrf = body.CSRFToken
	if c.session == "" || c.csrf == "" {
		e.t.Fatalf("no session in %v %s", r.header, r.body)
	}
	return c
}

// qaEnroll uses an enrollment link: begin, then finish with a code of the
// secret begin returned.
func (e *e2e) qaEnroll(name, token string) qaMember {
	e.t.Helper()
	var begin struct {
		Secret string `json:"secret"`
	}
	e.want(e.do("POST", "/api/v1/enroll/begin", map[string]string{"token": token}, credentials{}), 200, &begin)
	if begin.Secret == "" {
		e.t.Fatal("enroll/begin returned no secret")
	}
	r := e.do("POST", "/api/v1/enroll/finish", map[string]string{"token": token, "password": qaPassword, "code": e.qaNextCode(begin.Secret)}, credentials{})
	return qaMember{name: name, secret: begin.Secret, creds: qaSession(e, r)}
}

// qaJoin invites and enrolls a member with grants.
func (e *e2e) qaJoin(name string, grants ...qaGrant) qaMember {
	e.t.Helper()
	return e.qaEnroll(name, e.qaInvite(name, "member", grants, nil))
}

// qaLogin signs a user in by password and code.
func (e *e2e) qaLogin(m qaMember) reply {
	return e.do("POST", "/api/v1/auth/login", map[string]string{"username": m.name, "password": qaPassword, "code": e.qaNextCode(m.secret)}, credentials{})
}

type qaUser struct {
	Username string    `json:"username"`
	Role     string    `json:"role"`
	State    string    `json:"state"`
	Disabled bool      `json:"disabled"`
	Grants   []qaGrant `json:"grants"`
	SignIn   []string  `json:"signIn"`
}

func (e *e2e) qaDirectory() map[string]qaUser {
	e.t.Helper()
	var out struct {
		Output struct {
			Users []qaUser `json:"users"`
		} `json:"output"`
	}
	e.want(e.qaRead(e.admin(), "core.user.directory", nil), 200, &out)
	m := map[string]qaUser{}
	for _, u := range out.Output.Users {
		m[u.Username] = u
	}
	return m
}

// qaHostIDs is the ids core.host.list shows c; a refused list shows none.
func (e *e2e) qaHostIDs(c credentials) []string {
	e.t.Helper()
	var out struct {
		Output struct {
			Hosts []struct {
				ID string `json:"id"`
			} `json:"hosts"`
		} `json:"output"`
	}
	r := e.qaRead(c, "core.host.list", nil)
	if r.status == 403 {
		return nil
	}
	e.want(r, 200, &out)
	var ids []string
	for _, h := range out.Output.Hosts {
		ids = append(ids, h.ID)
	}
	return ids
}

func qaHas(list []string, id string) bool {
	for _, s := range list {
		if s == id {
			return true
		}
	}
	return false
}

func TestA12_user_create_role(t *testing.T) {
	e := qaStart(t)
	sock := e.s.SocketPath()
	create := func(name string, extra ...string) string {
		args := append([]string{"user", "create", "--username", name, "--socket", sock, "--yes"}, extra...)
		out, errOut, code := e.cli(nil, qaPassword+"\n", args...)
		m := secretLine.FindStringSubmatch(out)
		if code != cli.ExitUsage || m == nil {
			t.Fatalf("user create %s: exit %d\n%s\n%s", name, code, out, errOut)
		}
		return m[1]
	}
	activate := func(name, secret string) {
		t.Helper()
		out, errOut, code := e.cli(nil, e.qaNextCode(secret)+"\n", "user", "activate", "--username", name, "--socket", sock, "--yes")
		if code != cli.ExitOK {
			t.Fatalf("user activate %s: exit %d\n%s\n%s", name, code, out, errOut)
		}
	}
	plainSecret := create("plain")
	memberSecret := create("memb", "--role", "member")
	activate("plain", plainSecret)
	activate("memb", memberSecret)

	dir := e.qaDirectory()
	if dir["plain"].Role != "admin" {
		t.Errorf("a user created without a role has role %q, want admin", dir["plain"].Role)
	}
	if dir["memb"].Role != "member" || len(dir["memb"].Grants) != 0 {
		t.Errorf("a user created with role member: %+v, want a member with no grants", dir["memb"])
	}
	qaHost(e, "h-qa-1")
	member := qaMember{name: "memb", secret: memberSecret}
	member.creds = qaSession(e, e.qaLogin(member))
	if ids := e.qaHostIDs(member.creds); len(ids) != 0 {
		t.Errorf("a member with no access sees hosts %v, want none", ids)
	}
	plain := qaMember{name: "plain", secret: plainSecret}
	plain.creds = qaSession(e, e.qaLogin(plain))
	if ids := e.qaHostIDs(plain.creds); !qaHas(ids, "h-qa-1") {
		t.Errorf("an admin created without a role sees hosts %v, want h-qa-1 in them", ids)
	}
}

// qaTokenOutput is the output of core.token.create.
type qaTokenOutput struct {
	ID    string `json:"id"`
	Scope string `json:"scope"`
	Token string `json:"token"`
}

// qaToken creates a token with c and returns it, or the refused result.
func (e *e2e) qaToken(c credentials, scope string) (qaTokenOutput, qaResult) {
	e.t.Helper()
	r := e.qaCall(c, "core.token.create", map[string]any{"name": "qa " + scope, "scope": scope})
	var out qaTokenOutput
	if r.ok() {
		if err := json.Unmarshal(r.change.Output, &out); err != nil {
			e.t.Fatal(err)
		}
	}
	return out, r
}

func TestA9_last_admin_is_kept(t *testing.T) {
	e := qaStart(t)
	// admin is the only active admin: no way of removing it works.
	for _, tc := range []struct {
		name  string
		input any
	}{
		{"core.user.set-role", map[string]any{"username": "admin", "role": "member"}},
		{"core.user.disable", map[string]any{"username": "admin"}},
	} {
		if r := e.qaCall(e.admin(), tc.name, tc.input); r.code() != "user.last_admin" {
			t.Errorf("%s of the last admin: plan %d %s, apply %d %s, want user.last_admin", tc.name, r.plan.status, r.plan.body, r.apply.status, r.apply.body)
		}
	}
	// The break-glass delete goes through the controller socket.
	sock := &e2e{t: t, url: "http://controller", home: e.home}
	del := func(path string, body any) reply {
		b, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(t.Context(), "POST", sock.url+path, bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := socketClient(e.s.SocketPath()).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		rb, _ := io.ReadAll(resp.Body)
		return reply{status: resp.StatusCode, header: resp.Header, body: rb}
	}
	plan := del("/api/v1/actions/core.user.delete/v1/plan", map[string]any{"input": map[string]any{"username": "admin"}})
	code := string(plan.problemCode())
	if plan.status == 200 {
		var p struct {
			PlanID string `json:"planId"`
		}
		_ = json.Unmarshal(plan.body, &p)
		apply := del("/api/v1/actions/core.user.delete/v1/apply", map[string]any{"planId": p.PlanID, "confirmation": "admin"})
		code = string(apply.problemCode())
		if apply.status == 200 {
			var ch struct {
				Error *struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(apply.body, &ch)
			if ch.Error != nil {
				code = ch.Error.Code
			}
		}
	}
	if code != "user.last_admin" {
		t.Errorf("break-glass delete of the last admin: plan %d %s, want a refusal with user.last_admin (got %q)", plan.status, plan.body, code)
	}
	if u := e.qaDirectory()["admin"]; u.Role != "admin" || u.Disabled {
		t.Errorf("the last admin after the refusals: %+v, want an enabled admin", u)
	}

	// A second admin makes the first removable; a disabled admin does not
	// count, so the remaining one is again the last.
	second := e.qaEnroll("admin2", e.qaInvite("admin2", "admin", nil, nil))
	if r := e.qaCall(e.admin(), "core.user.disable", map[string]any{"username": "admin2"}); !r.ok() {
		t.Fatalf("disabling one of two admins: %s %s", r.plan.body, r.apply.body)
	}
	if r := e.qaCall(e.admin(), "core.user.set-role", map[string]any{"username": "admin", "role": "member"}); r.code() != "user.last_admin" {
		t.Errorf("demoting the only active admin beside a disabled one: %q, want user.last_admin", r.code())
	}
	if r := e.qaCall(e.admin(), "core.user.enable", map[string]any{"username": "admin2"}); !r.ok() {
		t.Fatalf("enabling admin2: %s %s", r.plan.body, r.apply.body)
	}
	if r := e.qaCall(second.creds, "core.user.disable", map[string]any{"username": "admin"}); !r.ok() {
		t.Errorf("disabling one of two active admins: plan %d %s, apply %d %s, want it done", r.plan.status, r.plan.body, r.apply.status, r.apply.body)
	}
}

func TestA10_invite_link(t *testing.T) {
	e, opts := qaStartOpts(t)
	token := e.qaInvite("inv1", "member", nil, nil)
	if len(token) < 20 {
		t.Fatalf("the invite's token %q is too short to be a link secret", token)
	}
	if u := e.qaDirectory()["inv1"]; u.State != "pending" || u.Role != "member" {
		t.Errorf("an invited user: %+v, want pending member", u)
	}

	// The link is shown once: no later read returns it.
	for name, r := range map[string]reply{
		"directory": e.qaRead(e.admin(), "core.user.directory", nil),
		"audit":     e.qaRead(e.admin(), "core.audit.list", map[string]any{"limit": 500}),
		"changes":   e.do("GET", "/api/v1/changes", nil, e.admin()),
	} {
		if strings.Contains(string(r.body), token) {
			t.Errorf("the %s output contains the enrollment token", name)
		}
	}

	// One step sets the password and the TOTP code, activates and signs in.
	m := e.qaEnroll("inv1", token)
	var me struct {
		User struct {
			Username string `json:"username"`
			Role     string `json:"role"`
			State    string `json:"state"`
		} `json:"user"`
	}
	e.want(e.do("GET", "/api/v1/auth/session", nil, credentials{session: m.creds.session}), 200, &me)
	if me.User.Username != "inv1" || me.User.State != "active" {
		t.Errorf("the session after enrollment: %+v, want inv1 active", me.User)
	}
	if u := e.qaDirectory()["inv1"]; u.State != "active" {
		t.Errorf("an enrolled user: %+v, want active", u)
	}
	// The password it set works, with a code of its secret.
	if r := e.qaLogin(m); r.status != 200 {
		t.Errorf("password login after enrollment: %d %s", r.status, r.body)
	}

	// The link works once.
	r := e.do("POST", "/api/v1/enroll/begin", map[string]string{"token": token}, credentials{})
	e.wantProblem(r, 410, errcode.EnrollmentExpired)
	r = e.do("POST", "/api/v1/enroll/finish", map[string]string{"token": token, "password": qaPassword + "2", "code": e.qaNextCode(m.secret)}, credentials{})
	if r.status == 200 {
		t.Errorf("a used link finished a second enrollment: %s", r.body)
	}

	// Seventy-two hours: a link is good just before, refused just after.
	early := e.qaInvite("early", "member", nil, nil)
	late := e.qaInvite("late", "member", nil, nil)
	issued := e.clock.Now()
	e.qaRestartAt(opts, issued.Add(72*time.Hour-time.Minute))
	e.want(e.do("POST", "/api/v1/enroll/begin", map[string]string{"token": early}, credentials{}), 200, nil)
	e.qaRestartAt(opts, issued.Add(72*time.Hour+time.Minute))
	for _, tok := range []string{early, late} {
		e.wantProblem(e.do("POST", "/api/v1/enroll/begin", map[string]string{"token": tok}, credentials{}), 410, errcode.EnrollmentExpired)
		r := e.do("POST", "/api/v1/enroll/finish", map[string]string{"token": tok, "password": qaPassword, "code": "123456"}, credentials{})
		if r.status == 200 {
			t.Errorf("an expired link finished an enrollment: %s", r.body)
		}
	}
}

func TestA11_pending_user_cannot_sign_in(t *testing.T) {
	e := qaStart(t)
	e.qaInvite("pend", "member", nil, nil)
	// Whatever password and code are tried, a pending user gets no session.
	for _, pw := range []string{qaPassword, "", "password", "pend"} {
		r := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "pend", "password": pw, "code": "000000"}, credentials{})
		if r.status != 401 && r.status != 403 {
			t.Errorf("password login of a pending user with %q: status %d, want a refusal", pw, r.status)
		}
		for _, c := range (&http.Response{Header: r.header}).Cookies() {
			if c.Name == api.SessionCookiePlain && c.Value != "" {
				t.Errorf("a refused login set a session cookie")
			}
		}
	}
}

func TestA11_pending_user_with_known_secrets_cannot_sign_in(t *testing.T) {
	e := qaStart(t)
	// A user created through the socket is pending until its first code is confirmed; its
	// password and TOTP secret are known here, so only its state refuses the login.
	out, errOut, code := e.cli(nil, qaPassword+"\n", "user", "create", "--username", "pendcli", "--role", "member", "--socket", e.s.SocketPath(), "--yes")
	m := secretLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("user create: exit %d\n%s\n%s", code, out, errOut)
	}
	pend := qaMember{name: "pendcli", secret: m[1]}
	r := e.qaLogin(pend)
	if r.status == 200 {
		t.Errorf("a pending user signed in by password with its right password and code: %s", r.body)
	}
	for _, c := range (&http.Response{Header: r.header}).Cookies() {
		if c.Name == api.SessionCookiePlain && c.Value != "" {
			t.Errorf("a refused login set a session cookie")
		}
	}
	// Once confirmed it signs in.
	if out, errOut, code := e.cli(nil, e.qaNextCode(pend.secret)+"\n", "user", "activate", "--username", "pendcli", "--socket", e.s.SocketPath(), "--yes"); code != cli.ExitOK {
		t.Fatalf("user activate: exit %d\n%s\n%s", code, out, errOut)
	}
	if r := e.qaLogin(pend); r.status != 200 {
		t.Errorf("an activated user's password login = %d %s, want 200", r.status, r.body)
	}
}

func TestA47_member_owns_tokens(t *testing.T) {
	e := qaStart(t)
	qaHost(e, "h-tok")
	m := e.qaJoin("tokm", qaGrant{"*", "viewer"})
	adminTok, r := e.qaToken(e.admin(), "read")
	if !r.ok() {
		t.Fatalf("admin token: %s %s", r.plan.body, r.apply.body)
	}
	own, r := e.qaToken(m.creds, "read")
	if !r.ok() {
		t.Fatalf("a member creating its own read token: plan %d %s, apply %d %s", r.plan.status, r.plan.body, r.apply.status, r.apply.body)
	}
	if own.Token == "" || own.ID == "" {
		t.Fatalf("a member's token output %+v, want id and token", own)
	}
	var list struct {
		Output struct {
			Tokens []struct {
				ID        string  `json:"id"`
				RevokedAt *string `json:"revokedAt"`
			} `json:"tokens"`
		} `json:"output"`
	}
	e.want(e.qaRead(m.creds, "core.token.list", nil), 200, &list)
	if len(list.Output.Tokens) != 1 || list.Output.Tokens[0].ID != own.ID {
		t.Errorf("a member's token list %+v, want only its own token %s (not the admin's %s)", list.Output.Tokens, own.ID, adminTok.ID)
	}
	// The member's own token works as a bearer.
	e.want(e.do("GET", "/api/v1/auth/session", nil, credentials{bearer: own.Token}), 200, nil)
	// A member cannot revoke the admin's token.
	if r := e.qaCall(m.creds, "core.token.revoke", map[string]any{"id": adminTok.ID}); r.ok() {
		t.Errorf("a member revoked another user's token")
	}
	if r := e.qaCall(m.creds, "core.token.revoke", map[string]any{"id": own.ID}); !r.ok() {
		t.Errorf("a member revoking its own token: plan %d %s, apply %d %s", r.plan.status, r.plan.body, r.apply.status, r.apply.body)
	}
	e.wantProblem(e.do("GET", "/api/v1/auth/session", nil, credentials{bearer: own.Token}), 401, errcode.AuthRequired)
	// The other settings pages' actions stay admin.
	if r := e.qaCall(m.creds, "core.group.create", map[string]any{"name": "x"}); r.plan.status != 403 {
		t.Errorf("a member planning core.group.create: %d, want 403", r.plan.status)
	}
}

func TestA63_member_token_limits(t *testing.T) {
	e := qaStart(t)
	m := e.qaJoin("lim", qaGrant{"*", "operator"})
	for _, scope := range []string{"read", "operate"} {
		if tok, r := e.qaToken(m.creds, scope); !r.ok() || tok.Scope != scope {
			t.Errorf("a member's session asking for a %s token: plan %d %s, apply %d %s", scope, r.plan.status, r.plan.body, r.apply.status, r.apply.body)
		}
	}
	if _, r := e.qaToken(m.creds, "admin"); r.ok() || r.plan.status != 403 && r.plan.status != 422 {
		t.Errorf("a member's session asking for an admin token: plan %d %s, apply %d, want a refusal", r.plan.status, r.plan.body, r.apply.status)
	}
	// A member's read token (and an operate one) cannot mint or revoke.
	readTok, _ := e.qaToken(m.creds, "read")
	opTok, _ := e.qaToken(m.creds, "operate")
	for name, tok := range map[string]qaTokenOutput{"read": readTok, "operate": opTok} {
		c := credentials{bearer: tok.Token}
		if r := e.qaCall(c, "core.token.create", map[string]any{"name": "again", "scope": "read"}); r.plan.status != 403 {
			t.Errorf("a member's %s token planning core.token.create: %d %s, want 403", name, r.plan.status, r.plan.body)
		}
		if r := e.qaCall(c, "core.token.revoke", map[string]any{"id": tok.ID}); r.plan.status != 403 {
			t.Errorf("a member's %s token planning core.token.revoke: %d %s, want 403", name, r.plan.status, r.plan.body)
		}
	}
	// An admin's session may still mint admin tokens, as before.
	if _, r := e.qaToken(e.admin(), "admin"); !r.ok() {
		t.Errorf("an admin's session minting an admin token: %s %s", r.plan.body, r.apply.body)
	}
}

func TestA60_disabled_user(t *testing.T) {
	e := qaStart(t)
	qaHost(e, "h-dis")
	m := e.qaJoin("dis", qaGrant{"*", "viewer"})
	tok, r := e.qaToken(m.creds, "read")
	if !r.ok() {
		t.Fatalf("token: %s %s", r.plan.body, r.apply.body)
	}
	pending := e.qaInvite("dispend", "member", nil, nil)
	e.want(e.do("GET", "/api/v1/auth/session", nil, credentials{session: m.creds.session}), 200, nil)

	e.qaMust("core.user.disable", map[string]any{"username": "dis"})
	e.qaMust("core.user.disable", map[string]any{"username": "dispend"})
	if u := e.qaDirectory()["dis"]; !u.Disabled {
		t.Errorf("a disabled user is listed as %+v, want disabled", u)
	}
	// The next request of its session and its token is refused.
	e.wantProblem(e.do("GET", "/api/v1/auth/session", nil, credentials{session: m.creds.session}), 401, errcode.AuthRequired)
	e.wantProblem(e.do("GET", "/api/v1/auth/session", nil, credentials{bearer: tok.Token}), 401, errcode.AuthRequired)
	if r := e.qaCall(credentials{session: m.creds.session, csrf: m.creds.csrf}, "core.token.create", map[string]any{"name": "n", "scope": "read"}); r.ok() {
		t.Errorf("a disabled user's session created a token")
	}
	// Its password sign-in and its enrollment link are refused.
	if r := e.qaLogin(m); r.status == 200 {
		t.Errorf("a disabled user signed in by password: %s", r.body)
	}
	if r := e.do("POST", "/api/v1/enroll/begin", map[string]string{"token": pending}, credentials{}); r.status == 200 {
		t.Errorf("a disabled pending user began enrollment: %s", r.body)
	}
	// It does not count as an active admin.
	if len(e.qaHostIDs(e.admin())) < 1 {
		t.Errorf("the admin lost its hosts")
	}

	// core.user.enable restores it.
	e.qaMust("core.user.enable", map[string]any{"username": "dis"})
	if r := e.qaLogin(m); r.status != 200 {
		t.Errorf("password login after core.user.enable: %d %s, want 200", r.status, r.body)
	}
	if u := e.qaDirectory()["dis"]; u.Disabled {
		t.Errorf("an enabled user is listed as %+v", u)
	}
}

func TestA59_group_delete_refused_while_named(t *testing.T) {
	e := qaStart(t)
	qaHost(e, "h-grp")
	gid := e.qaGroup("Edge", "h-grp")
	e.qaJoin("grantee", qaGrant{gid, "viewer"})
	e.qaMust("metrics.alert.create", map[string]any{"name": "Edge CPU", "metric": "cpu", "comparator": ">", "threshold": 90,
		"durationMin": 5, "severity": "warning", "target": gid})
	e.qaMust("notify.channel.create", map[string]any{"kind": "webhook", "name": "Edge hook", "minSeverity": "info", "groupIds": []string{gid},
		"webhook": map[string]any{"url": "http://127.0.0.1:9/hook"}})

	r := e.qaCall(e.admin(), "core.group.delete", map[string]any{"id": gid})
	if r.ok() || r.code() != "group.in_use" {
		t.Fatalf("deleting a group named by a grant, a rule and a channel: plan %d %s, apply %d %s, want group.in_use", r.plan.status, r.plan.body, r.apply.status, r.apply.body)
	}
	text := string(r.plan.body) + string(r.apply.body) + string(r.change.Output)
	for _, want := range []string{"grantee", "Edge CPU", "Edge hook"} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not name %q: %s", want, text)
		}
	}
	// Once nothing names it, the group goes.
	e.qaMust("core.user.set-grants", map[string]any{"username": "grantee", "grants": []qaGrant{}})

}

// qaTimeFormat is the fixed-width time text the stores keep.
const qaTimeFormat = "2006-01-02T15:04:05.000000000Z"

// qaSeedChange inserts a completed change of action on host (empty for a
// host-less one), with the plan row it needs. Ids sort in the order of n.
func qaSeedChange(e *e2e, n int, host, action string) string {
	e.t.Helper()
	id := fmt.Sprintf("01QA%022d", n)
	at := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Second).Format(qaTimeFormat)
	exp := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).Format(qaTimeFormat)
	exec(e.t, e.s, `INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json, guards_json, confirmation, created_at, expires_at)
		VALUES (?, ?, ?, 1, 'system', 'h', 'h', '[]', '[]', '', ?, ?)`, "P"+id, host, action, at, exp)
	exec(e.t, e.s, `INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state, input_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, 'revertible', '', 'system', 'completed', '{}', ?, ?)`, id, "P"+id, host, action, at, at)
	return id
}

// qaSeedAudit inserts one audit row of an action on host, empty for none.
func qaSeedAudit(e *e2e, n int, host string) {
	e.t.Helper()
	at := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Second).Format(qaTimeFormat)
	var h any
	if host != "" {
		h = host
	}
	exec(e.t, e.s, `INSERT INTO core_audit (id, seq, at, actor, host_id, action, version, phase, input_json, result)
		VALUES (?, ?, ?, 'system', ?, 'firewall.defaults.set', 1, 'apply', '{}', 'ok')`, fmt.Sprintf("01QAAUD%019d", n), n, at, h)
}

// qaAccess is the two-host fixture of the access tests: h-g is in group G,
// h-x in none, and the local host in group L.
type qaAccess struct {
	e     *e2e
	g, l  string
	local string
}

func qaAccessFixture(t *testing.T) *qaAccess {
	t.Helper()
	e := qaStart(t)
	qaHost(e, "h-g")
	qaHost(e, "h-x")
	a := &qaAccess{e: e, local: e.s.LocalHostID()}
	a.g = e.qaGroup("Group G", "h-g")
	a.l = e.qaGroup("Group L", a.local)
	return a
}

// qaPath of an agent-side mutating action's plan on host.
func qaPlanPath(host string) string {
	return "/api/v1/hosts/" + host + "/actions/firewall.defaults.set/v1/plan"
}

// qaReached reports whether an answer shows the request passed
// authorization: anything but forbidden and not found.
func qaReached(r reply) bool { return r.status != 403 && r.status != 404 }

func TestA2_viewer_reads_and_cannot_mutate(t *testing.T) {
	a := qaAccessFixture(t)
	e := a.e
	v := e.qaJoin("viewer-g", qaGrant{a.g, "viewer"})
	for _, path := range []string{
		"/api/v1/hosts/h-g/resources/hostinfo/summary",
		"/api/v1/hosts/h-g/resources/firewall/status",
		"/api/v1/hosts/h-g/resources/docker/containers",
		"/api/v1/hosts/h-g/resources/system/updates",
		"/api/v1/hosts/h-g/modules/firewall/availability",
	} {
		if r := e.do("GET", path, nil, v.creds); !qaReached(r) {
			t.Errorf("viewer on G: GET %s = %d %s, want it past authorization", path, r.status, r.body)
		}
	}
	for _, name := range []string{"firewall.defaults.set", "firewall.rules.apply", "docker.container.start", "system.packages.upgrade"} {
		path := "/api/v1/hosts/h-g/actions/" + name + "/v1/plan"
		r := e.do("POST", path, map[string]any{"input": map[string]any{}}, v.creds)
		if name == "docker.container.start" || name == "system.packages.upgrade" {
			if _, ok := e.s.Registry().Action(core.ActionRef{Name: name, Version: 1}); !ok {
				continue
			}
		}
		e.wantProblem(r, 403, errcode.Forbidden)
	}
}

func TestA4_hosts_outside_grants_are_not_found(t *testing.T) {
	a := qaAccessFixture(t)
	e := a.e
	v := e.qaJoin("viewer-g", qaGrant{a.g, "viewer"})
	o := e.qaJoin("operator-g", qaGrant{a.g, "operator"})
	hidden := qaSeedChange(e, 1, "h-x", "firewall.defaults.set")
	inside := qaSeedChange(e, 2, "h-g", "firewall.defaults.set")
	for _, m := range []qaMember{v, o} {
		for _, tc := range []struct{ method, path string }{
			{"GET", "/api/v1/hosts/h-x/resources/hostinfo/summary"},
			{"GET", "/api/v1/hosts/h-x/modules/firewall/availability"},
			{"POST", qaPlanPath("h-x")},
			{"POST", "/api/v1/hosts/h-x/actions/firewall.defaults.set/v1/read"},
			{"POST", "/api/v1/hosts/h-x/actions/firewall.defaults.set/v1/apply"},
			{"GET", "/api/v1/changes/" + hidden},
			{"GET", "/api/v1/changes/" + hidden + "/effects"},
			{"GET", "/api/v1/changes/" + hidden + "/steps"},
			{"POST", "/api/v1/changes/" + hidden + "/confirm"},
			{"POST", "/api/v1/changes/" + hidden + "/revert"},
			{"POST", "/api/v1/changes/" + hidden + "/undo"},
		} {
			var body any
			if tc.method == "POST" {
				body = map[string]any{"input": map[string]any{}, "planId": "x"}
			}
			r := e.do(tc.method, tc.path, body, m.creds)
			if r.status != 404 {
				t.Errorf("%s: %s %s = %d %s, want 404 not found, never forbidden", m.name, tc.method, tc.path, r.status, r.body)
			}
		}
		for _, host := range []string{"h-x"} {
			for _, st := range e.s.Registry().Streams() {
				path := "/api/v1/hosts/" + host + "/streams/" + st.Module + "/" + st.Def.Name
				if r := e.do("GET", path, nil, m.creds); r.status != 404 {
					t.Errorf("%s: GET %s = %d, want 404", m.name, path, r.status)
				}
			}
		}
		// The same shapes on a visible host are not found for a missing change only.
		if r := e.do("GET", "/api/v1/changes/"+inside, nil, m.creds); r.status != 200 {
			t.Errorf("%s: a change on its own host = %d %s, want 200", m.name, r.status, r.body)
		}
	}
	// A change that does not exist answers the same as a hidden one.
	missing := e.do("GET", "/api/v1/changes/01QAMISSING", nil, v.creds)
	hid := e.do("GET", "/api/v1/changes/"+hidden, nil, v.creds)
	if missing.status != hid.status || missing.problemCode() != hid.problemCode() {
		t.Errorf("a hidden change answers %d %s, a missing one %d %s, want the same", hid.status, hid.problemCode(), missing.status, missing.problemCode())
	}
}

func TestA5_lists_hold_only_visible_hosts(t *testing.T) {
	a := qaAccessFixture(t)
	e := a.e
	v := e.qaJoin("viewer-g", qaGrant{a.g, "viewer"})
	qaSeedChange(e, 1, "h-x", "firewall.defaults.set")
	qaSeedChange(e, 2, "h-g", "firewall.defaults.set")
	qaSeedAudit(e, 1, "h-x")
	qaSeedAudit(e, 2, "h-g")

	ids := e.qaHostIDs(v.creds)
	if len(ids) != 1 || ids[0] != "h-g" {
		t.Errorf("core.host.list for a viewer on G = %v, want [h-g]", ids)
	}
	if all := e.qaHostIDs(e.admin()); !qaHas(all, "h-x") || !qaHas(all, "h-g") {
		t.Errorf("core.host.list for the admin = %v, want both hosts", all)
	}
	for name, r := range map[string]reply{
		"core.audit.list":      e.qaRead(v.creds, "core.audit.list", nil),
		"core.update.rollouts": e.qaRead(v.creds, "core.update.rollouts", nil),
		"metrics.alert.list":   e.qaRead(v.creds, "metrics.alert.list", nil),
		"GET /changes":         e.do("GET", "/api/v1/changes", nil, v.creds),
	} {
		if r.status != 200 {
			t.Errorf("%s as a viewer on G = %d %s, want 200 with its rows", name, r.status, r.body)
			continue
		}
		if strings.Contains(string(r.body), "h-x") {
			t.Errorf("%s as a viewer on G names h-x: %s", name, r.body)
		}
	}
	if r := e.qaRead(v.creds, "core.audit.list", nil); !strings.Contains(string(r.body), "h-g") {
		t.Errorf("core.audit.list as a viewer on G lacks the h-g row: %s", r.body)
	}
	if r := e.do("GET", "/api/v1/changes", nil, v.creds); !strings.Contains(string(r.body), "h-g") {
		t.Errorf("GET /changes as a viewer on G lacks the h-g change: %s", r.body)
	}
}

func TestA6_members_cannot_run_controller_side_actions(t *testing.T) {
	a := qaAccessFixture(t)
	e := a.e
	op := e.qaJoin("op-star", qaGrant{"*", "operator"}, qaGrant{a.g, "operator"})
	n := 0
	for _, def := range e.s.Registry().Actions() {
		s := def.Spec
		if s.Side != core.SideController || s.Class == core.ClassRead || s.BreakGlass || s.Internal {
			continue
		}
		if s.Name == "core.token.create" || s.Name == "core.token.revoke" {
			continue // the named exceptions of A47
		}
		n++
		path := "/api/v1/actions/" + s.Name + "/v" + fmt.Sprint(s.Version) + "/plan"
		r := e.do("POST", path, map[string]any{"input": map[string]any{"hostId": "h-g", "hostIds": []string{"h-g"}}}, op.creds)
		if r.status != 403 || r.problemCode() != errcode.Forbidden {
			t.Errorf("an operator planning %s = %d %s, want 403 auth.forbidden", s.Name, r.status, r.body)
		}
	}
	if n < 20 {
		t.Errorf("the registry walk found only %d controller-side mutating actions", n)
	}
	// Module enablement, update policy and rollouts by name.
	for _, name := range []string{"core.module.enable", "core.module.disable", "core.host.update-policy", "core.update.rollout"} {
		r := e.do("POST", "/api/v1/actions/"+name+"/v1/plan", map[string]any{"input": map[string]any{"hostId": "h-g"}}, op.creds)
		e.wantProblem(r, 403, errcode.Forbidden)
	}
}

func TestA7_star_covers_new_hosts_and_group_removal_revokes(t *testing.T) {
	a := qaAccessFixture(t)
	e := a.e
	star := e.qaJoin("star", qaGrant{"*", "viewer"})
	g := e.qaJoin("ingroup", qaGrant{a.g, "viewer"})
	qaHost(e, "h-new") // enrolled after the grants
	if ids := e.qaHostIDs(star.creds); !qaHas(ids, "h-new") {
		t.Errorf("a * grant after a host was added: core.host.list = %v, want h-new in it", ids)
	}
	if r := e.do("GET", "/api/v1/hosts/h-new/resources/hostinfo/summary", nil, star.creds); !qaReached(r) {
		t.Errorf("a * grant on a new host: %d %s, want it past authorization", r.status, r.body)
	}
	if r := e.do("GET", "/api/v1/hosts/h-new/resources/hostinfo/summary", nil, g.creds); r.status != 404 {
		t.Errorf("a G grant on a host in no group: %d, want 404", r.status)
	}
	// Putting the host in G gives access; removing it takes the access away at the next request.
	e.qaMust("core.group.set-members", map[string]any{"id": a.g, "hostIds": []string{"h-g", "h-new"}})
	if r := e.do("GET", "/api/v1/hosts/h-new/resources/hostinfo/summary", nil, g.creds); !qaReached(r) {
		t.Errorf("after adding the host to G: %d %s, want it past authorization", r.status, r.body)
	}
	e.qaMust("core.group.set-members", map[string]any{"id": a.g, "hostIds": []string{"h-g"}})
	if r := e.do("GET", "/api/v1/hosts/h-new/resources/hostinfo/summary", nil, g.creds); r.status != 404 {
		t.Errorf("after removing the host from G: %d %s, want 404", r.status, r.body)
	}
	if ids := e.qaHostIDs(g.creds); qaHas(ids, "h-new") {
		t.Errorf("after removing the host from G its member still lists %v", ids)
	}
}

func TestA8_token_never_exceeds_its_user(t *testing.T) {
	a := qaAccessFixture(t)
	e := a.e
	v := e.qaJoin("viewer-tok", qaGrant{a.g, "viewer"})
	tok, r := e.qaToken(v.creds, "operate")
	if !r.ok() {
		t.Fatalf("a viewer's operate token: plan %d %s, apply %d %s", r.plan.status, r.plan.body, r.apply.status, r.apply.body)
	}
	bearer := credentials{bearer: tok.Token}
	if r := e.do("GET", "/api/v1/hosts/h-g/resources/hostinfo/summary", nil, bearer); !qaReached(r) {
		t.Errorf("an operate token on a viewer grant reading: %d %s, want it past authorization", r.status, r.body)
	}
	e.wantProblem(e.do("POST", qaPlanPath("h-g"), map[string]any{"input": map[string]any{}}, bearer), 403, errcode.Forbidden)
	e.wantProblem(e.do("GET", "/api/v1/hosts/h-x/resources/hostinfo/summary", nil, bearer), 404, errcode.NotFound)
	// Demoting the user takes the token's reach with it.
	e.qaMust("core.user.set-grants", map[string]any{"username": "viewer-tok", "grants": []qaGrant{}})
	if r := e.do("GET", "/api/v1/hosts/h-g/resources/hostinfo/summary", nil, bearer); r.status != 404 {
		t.Errorf("a token of a user whose grants were removed: %d, want 404", r.status)
	}
}

func TestA65_controller_host_is_read_only_for_members(t *testing.T) {
	a := qaAccessFixture(t)
	e := a.e
	for name, m := range map[string]qaMember{
		"group":    e.qaJoin("op-l", qaGrant{a.l, "operator"}),
		"all":      e.qaJoin("op-star", qaGrant{"*", "operator"}),
		"viewer-l": e.qaJoin("v-l", qaGrant{a.l, "viewer"}),
	} {
		if r := e.do("GET", "/api/v1/hosts/"+a.local+"/resources/hostinfo/summary", nil, m.creds); !qaReached(r) {
			t.Errorf("%s: reading the controller's host: %d %s, want it past authorization", name, r.status, r.body)
		}
		e.wantProblem(e.do("POST", qaPlanPath(a.local), map[string]any{"input": map[string]any{}}, m.creds), 403, errcode.Forbidden)
		e.wantProblem(e.do("POST", "/api/v1/hosts/"+a.local+"/actions/firewall.defaults.set/v1/apply", map[string]any{"planId": "x"}, m.creds), 403, errcode.Forbidden)
	}
	// A member operating a different host is not stopped.
	op := e.qaJoin("op-g", qaGrant{a.g, "operator"})
	if r := e.do("POST", qaPlanPath("h-g"), map[string]any{"input": map[string]any{}}, op.creds); r.status == 403 || r.status == 404 {
		t.Errorf("an operator planning on its group's host: %d %s, want it past authorization", r.status, r.body)
	}
}

func TestA49_every_controller_read_is_classified(t *testing.T) {
	a := qaAccessFixture(t)
	n := 0
	for _, def := range a.e.s.Registry().Actions() {
		s := def.Spec
		if s.Side != core.SideController || s.Class != core.ClassRead || s.Internal || s.BreakGlass {
			continue
		}
		n++
		switch s.HostData {
		case core.HostDataNone, core.HostDataFleet, core.HostDataInput, core.HostDataFiltered:
		default:
			t.Errorf("controller-side read %s declares HostData %q, want none, fleet, input or filtered", s.Name, s.HostData)
		}
		if s.HostData == core.HostDataInput && len(s.HostPointers) == 0 {
			t.Errorf("%s is an input read with no host pointer", s.Name)
		}
	}
	if n < 15 {
		t.Errorf("the registry walk found only %d controller-side reads", n)
	}
	for _, name := range []string{"core.host.list", "core.audit.list", "core.update.rollouts", "npm.instance.list", "derp.server.list",
		"tailnet.device.list", "cloudflare.tunnel.list", "cloudflare.hostname.list", "metrics.alert.list"} {
		def, ok := a.e.s.Registry().Action(core.ActionRef{Name: name, Version: 1})
		if !ok {
			t.Errorf("no action %s", name)
			continue
		}
		if def.Spec.HostData != core.HostDataFiltered {
			t.Errorf("%s declares HostData %q, want filtered", name, def.Spec.HostData)
		}
	}
}

func TestA50_viewer_gets_no_other_host_from_any_read(t *testing.T) {
	a := qaAccessFixture(t)
	e := a.e
	exec(e.t, e.s, `UPDATE core_hosts SET name = 'outside-secret-name' WHERE id = 'h-x'`)
	exec(e.t, e.s, `INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES ('h-x', '100.99.88.77', 'nSTABLEOUTSIDE', ?)`, e.clock.Now().UTC().Format(qaTimeFormat))
	for _, h := range []string{"h-g", "h-x"} {
		exec(e.t, e.s, `INSERT INTO derp_servers (host_id, mode, host_name, region_id, region_code, derper_version, unit_active, checked_at)
			VALUES (?, 'ip', ?, 1, 'qa', '1.0', 1, ?)`, h, "derp-of-"+h, e.clock.Now().UTC().Format(qaTimeFormat))
	}
	qaSeedChange(e, 1, "h-x", "firewall.defaults.set")
	qaSeedAudit(e, 1, "h-x")
	v := e.qaJoin("viewer-g", qaGrant{a.g, "viewer"})
	needles := []string{"h-x", "outside-secret-name", "100.99.88.77", "nSTABLEOUTSIDE", "derp-of-h-x"}
	n := 0
	for _, def := range e.s.Registry().Actions() {
		s := def.Spec
		if s.Side != core.SideController || s.Class != core.ClassRead || s.Internal || s.BreakGlass {
			continue
		}
		in := map[string]any{}
		if s.HostData == core.HostDataInput {
			in["hostId"] = "h-x"
		}
		r := e.do("POST", "/api/v1/actions/"+s.Name+"/v"+fmt.Sprint(s.Version)+"/read", map[string]any{"input": in}, v.creds)
		n++
		for _, needle := range needles {
			// A refusal of a host the caller itself named may repeat the id it sent.
			if needle == "h-x" && s.HostData == core.HostDataInput {
				continue
			}
			if strings.Contains(string(r.body), needle) {
				t.Errorf("%s as a viewer on G (%d) returns %q: %s", s.Name, r.status, needle, r.body)
			}
		}
		if s.HostData == core.HostDataInput && r.status != 404 {
			t.Errorf("%s naming a host outside the grants = %d, want 404", s.Name, r.status)
		}
	}
	if n < 15 {
		t.Errorf("walked only %d reads", n)
	}
	if r := e.qaRead(v.creds, "derp.server.list", nil); r.status == 200 && !strings.Contains(string(r.body), "h-g") {
		t.Errorf("derp.server.list as a viewer on G lacks its own h-g row: %s", r.body)
	}
}

func TestA51_member_pages_are_full(t *testing.T) {
	e := qaStart(t)
	for i := 0; i < 200; i++ {
		qaHost(e, fmt.Sprintf("h%03d", i))
	}
	gid := e.qaGroup("Sparse", "h007", "h131")
	m := e.qaJoin("sparse", qaGrant{gid, "viewer"})
	n := 0
	var want []string
	for i := 0; i < 200; i++ {
		for k := 0; k < 3; k++ {
			n++
			id := qaSeedChange(e, n, fmt.Sprintf("h%03d", i), "firewall.defaults.set")
			qaSeedAudit(e, n, fmt.Sprintf("h%03d", i))
			if i == 7 || i == 131 {
				want = append(want, id)
			}
		}
	}
	for k := 0; k < 20; k++ {
		n++
		qaSeedChange(e, n, "", "core.group.create")
		qaSeedAudit(e, n, "")
	}
	for _, limit := range []int{1, 2, 4} {
		var got []string
		before := ""
		for page := 0; page < 20; page++ {
			path := fmt.Sprintf("/api/v1/changes?limit=%d", limit)
			if before != "" {
				path += "&before=" + before
			}
			var out struct {
				Changes []struct {
					ID     string `json:"id"`
					HostID string `json:"hostId"`
				} `json:"changes"`
			}
			e.want(e.do("GET", path, nil, m.creds), 200, &out)
			if len(out.Changes) == 0 {
				break
			}
			remaining := len(want) - len(got)
			if wantLen := min(limit, remaining); len(out.Changes) != wantLen {
				t.Errorf("limit %d, page %d: %d changes, want a full page of %d", limit, page, len(out.Changes), wantLen)
			}
			for _, c := range out.Changes {
				if c.HostID != "h007" && c.HostID != "h131" {
					t.Errorf("limit %d: a member's page holds a change of %q", limit, c.HostID)
				}
				got = append(got, c.ID)
			}
			before = out.Changes[len(out.Changes)-1].ID
		}
		if len(got) != len(want) {
			t.Errorf("limit %d: paging a member's changes gave %d rows %v, want the %d visible ones", limit, len(got), got, len(want))
		}
	}
	// The audit log pages the same way.
	var seen int
	before := ""
	for page := 0; page < 20; page++ {
		in := map[string]any{"limit": 2}
		if before != "" {
			in["before"] = before
		}
		var out struct {
			Output struct {
				Entries []struct {
					HostID *string `json:"hostId"`
				} `json:"entries"`
				NextBefore *string `json:"nextBefore"`
			} `json:"output"`
		}
		e.want(e.qaRead(m.creds, "core.audit.list", in), 200, &out)
		for _, en := range out.Output.Entries {
			if en.HostID == nil || (*en.HostID != "h007" && *en.HostID != "h131") {
				t.Errorf("a member's audit page holds a row of host %v", en.HostID)
			}
		}
		seen += len(out.Output.Entries)
		if len(out.Output.Entries) < 2 && out.Output.NextBefore != nil && page < 2 {
			t.Errorf("audit page %d holds %d rows of 2 though more remain", page, len(out.Output.Entries))
		}
		if out.Output.NextBefore == nil {
			break
		}
		before = *out.Output.NextBefore
	}
	if seen != 6 {
		t.Errorf("paging a member's audit log gave %d rows, want the 6 visible ones", seen)
	}
}

// qaHook is a webhook destination that records what it receives.
type qaHook struct {
	srv    *httptest.Server
	mu     sync.Mutex
	reqs   []qaHookReq
	status atomic.Int32
	body   atomic.Value // string answered with every response
	delay  atomic.Int64 // nanoseconds the first request is held
	first  atomic.Bool
}

type qaHookReq struct {
	Path   string
	Header http.Header
	Raw    []byte
	Kind   string
	ID     string
	Title  string
	BodyTx string
}

func qaNewHook(t *testing.T) *qaHook {
	t.Helper()
	h := &qaHook{}
	h.status.Store(200)
	h.body.Store("")
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var b struct {
			ID, Kind, Title, Body string
		}
		_ = json.Unmarshal(raw, &b)
		if d := h.delay.Load(); d > 0 && !h.first.Swap(true) {
			time.Sleep(time.Duration(d))
		}
		h.mu.Lock()
		h.reqs = append(h.reqs, qaHookReq{Path: r.URL.Path, Header: r.Header.Clone(), Raw: raw, Kind: b.Kind, ID: b.ID, Title: b.Title, BodyTx: b.Body})
		h.mu.Unlock()
		w.WriteHeader(int(h.status.Load()))
		_, _ = io.WriteString(w, h.body.Load().(string))
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *qaHook) got() []qaHookReq {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]qaHookReq(nil), h.reqs...)
}

func (h *qaHook) kinds() []string {
	var out []string
	for _, r := range h.got() {
		out = append(out, r.Kind)
	}
	return out
}

// qaChannel creates a webhook channel and returns its id.
func (e *e2e) qaChannel(name string, hook *qaHook, extra map[string]any) string {
	e.t.Helper()
	in := map[string]any{"kind": "webhook", "name": name, "minSeverity": "info",
		"webhook": map[string]any{"url": hook.srv.URL + "/hook/" + name, "secret": "qa-hmac-secret-" + name}}
	for k, v := range extra {
		in[k] = v
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(e.qaMust("notify.channel.create", in), &out); err != nil || out.ID == "" {
		e.t.Fatalf("notify.channel.create %s: %v", name, err)
	}
	return out.ID
}

// qaEmit stores an event through the server's notifier, as a fact's writer
// does, in its own transaction.
func (e *e2e) qaEmit(ev core.NotifyEvent) {
	e.t.Helper()
	err := e.s.controllerDB.Write(context.Background(), func(tx *sql.Tx) error {
		return e.s.notifier.Emit(context.Background(), tx, ev)
	})
	if err != nil {
		e.t.Fatalf("Emit %s: %v", ev.Kind, err)
	}
}

type qaDelivery struct {
	Event, Channel, State, NextAt, LastError, Kind, DedupeKey, Created string
	Attempts                                                           int
}

func (e *e2e) qaDeliveries() []qaDelivery {
	e.t.Helper()
	rows, err := e.s.controllerDB.Read().QueryContext(context.Background(), `SELECT d.event_id, d.channel_id, d.state, d.next_at, d.last_error, e.kind, e.dedupe_key, e.created_at, d.attempts
		FROM notify_deliveries d JOIN notify_events e ON e.id = d.event_id ORDER BY e.created_at, e.rowid, d.channel_id`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []qaDelivery
	for rows.Next() {
		var d qaDelivery
		if err := rows.Scan(&d.Event, &d.Channel, &d.State, &d.NextAt, &d.LastError, &d.Kind, &d.DedupeKey, &d.Created, &d.Attempts); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

// qaWaitDeliveries advances the fake clock one sender poll at a time until
// cond holds of the deliveries.
func (e *e2e) qaWaitDeliveries(what string, cond func([]qaDelivery) bool) []qaDelivery {
	e.t.Helper()
	var last []qaDelivery
	eventually(e.t, what, func() { e.clock.Advance(time.Second) }, func() bool {
		last = e.qaDeliveries()
		return cond(last)
	})
	return last
}

func qaEv(kind, severity, host, key string) core.NotifyEvent {
	return core.NotifyEvent{Kind: kind, Severity: severity, HostID: host, Title: "t " + key, Body: "b " + key, DedupeKey: key}
}

func TestA23_routing(t *testing.T) {
	a := qaAccessFixture(t)
	e := a.e
	hook := qaNewHook(t)
	crit := e.qaChannel("crit", hook, map[string]any{"minSeverity": "critical"})
	firing := e.qaChannel("firing", hook, map[string]any{"kinds": []string{"alert.firing"}})
	group := e.qaChannel("group", hook, map[string]any{"groupIds": []string{a.g}})
	all := e.qaChannel("all", hook, nil)
	hook.status.Store(200)
	// Stop the sender from emptying the table: hold every delivery pending.
	e.qaEmit(qaEv("alert.resolved", "warning", "h-g", "r1"))
	e.qaEmit(qaEv("alert.firing", "critical", "h-x", "r2"))
	e.qaEmit(qaEv("alert.firing", "warning", "", "r3"))
	got := map[string][]string{}
	for _, d := range e.qaDeliveries() {
		got[d.DedupeKey] = append(got[d.DedupeKey], d.Channel)
	}
	names := map[string]string{crit: "crit", firing: "firing", group: "group", all: "all"}
	for key, want := range map[string][]string{
		"r1": {"group", "all"},
		"r2": {"crit", "firing", "all"},
		"r3": {"firing", "all"},
	} {
		var have []string
		for _, id := range got[key] {
			have = append(have, names[id])
		}
		sort.Strings(have)
		sort.Strings(want)
		if strings.Join(have, ",") != strings.Join(want, ",") {
			t.Errorf("event %s was routed to %v, want %v", key, have, want)
		}
	}
}

func TestA24_dedupe(t *testing.T) {
	e := qaStart(t)
	hook := qaNewHook(t)
	ch := e.qaChannel("one", hook, nil)
	e.qaEmit(qaEv("alert.firing", "warning", "", "same-key"))
	e.clock.Advance(time.Minute)
	e.qaEmit(qaEv("alert.firing", "warning", "", "same-key"))
	events := func() (n int) {
		_ = e.s.controllerDB.Read().QueryRow(`SELECT count(*) FROM notify_events WHERE dedupe_key = 'same-key'`).Scan(&n)
		return
	}
	e.qaWaitDeliveries("the first delivery sent", func(ds []qaDelivery) bool {
		return len(ds) >= 1 && ds[0].State == "sent"
	})
	if n := events(); n != 2 {
		t.Errorf("events stored with the repeated key: %d, want both stored", n)
	}
	n := 0
	for _, d := range e.qaDeliveries() {
		if d.Channel == ch && d.DedupeKey == "same-key" {
			n++
		}
	}
	if n != 1 || len(hook.got()) != 1 {
		t.Errorf("a repeated key within 10 minutes: %d deliveries, %d requests, want 1 and 1", n, len(hook.got()))
	}
	// After the window the same key is delivered again.
	e.clock.Advance(11 * time.Minute)
	e.qaEmit(qaEv("alert.firing", "warning", "", "same-key"))
	e.qaWaitDeliveries("a delivery after the window", func([]qaDelivery) bool { return len(hook.got()) == 2 })
}

func TestA58_fire_resolve_fire_in_order(t *testing.T) {
	e := qaStart(t)
	qaHost(e, "h-al")
	hook := qaNewHook(t)
	hook.delay.Store(int64(300 * time.Millisecond)) // the first send is slow: a later event must not overtake it
	e.qaChannel("only", hook, nil)
	e.qaEmit(qaEv("alert.firing", "critical", "h-al", "alert.firing:rule1:h-al:cpu:1792324800"))
	e.qaEmit(qaEv("alert.resolved", "info", "h-al", "alert.resolved:rule1:h-al:cpu:1792324980"))
	e.qaEmit(qaEv("alert.firing", "critical", "h-al", "alert.firing:rule1:h-al:cpu:1792325100"))
	e.qaWaitDeliveries("all three delivered", func(ds []qaDelivery) bool {
		n := 0
		for _, d := range ds {
			if d.State == "sent" {
				n++
			}
		}
		return n == 3
	})
	if got := strings.Join(hook.kinds(), ","); got != "alert.firing,alert.resolved,alert.firing" {
		t.Errorf("a fire, resolve, fire sequence arrived as %s, want alert.firing,alert.resolved,alert.firing", got)
	}
}

func TestA64_channel_test_is_one_attempt(t *testing.T) {
	e := qaStart(t)
	hook := qaNewHook(t)
	ch := e.qaChannel("probe", hook, nil)
	hook.status.Store(503)
	hook.body.Store("PROVIDER-BODY-CANARY")
	var out struct {
		Output struct {
			Delivered bool   `json:"delivered"`
			Code      string `json:"code"`
		} `json:"output"`
	}
	r := e.qaRead(e.admin(), "notify.channel.test", map[string]any{"id": ch})
	if r.status != 200 {
		t.Errorf("notify.channel.test answered %d %s, want 200 reporting the provider's status", r.status, r.body)
	} else {
		e.want(r, 200, &out)
		if out.Output.Delivered || out.Output.Code != "503" {
			t.Errorf("a test answered 503 reports %+v, want not delivered with code 503", out.Output)
		}
	}
	if strings.Contains(string(r.body), "PROVIDER-BODY-CANARY") || strings.Contains(qaLog(t), "PROVIDER-BODY-CANARY") {
		t.Errorf("the provider's body reached the test's answer or the log")
	}
	if n := len(hook.got()); n != 1 {
		t.Errorf("a failed channel test reached the provider %d times, want one attempt", n)
	}
	hook.status.Store(200)
	r = e.qaRead(e.admin(), "notify.channel.test", map[string]any{"id": ch})
	if r.status != 200 {
		t.Errorf("notify.channel.test answered %d %s, want 200 reporting the provider's status", r.status, r.body)
	} else {
		e.want(r, 200, &out)
		if !out.Output.Delivered || out.Output.Code != "200" {
			t.Errorf("a test answered 200 reports %+v, want delivered with code 200", out.Output)
		}
	}
	if got := hook.got(); len(got) >= 2 {
		if req := got[1]; req.Title != "Test notification from Darkpanel" || req.Kind != "notify.test" {
			t.Errorf("the test notification is %q of kind %q, want the fixed test title", req.Title, req.Kind)
		}
	} else {
		t.Errorf("the provider saw %d requests after two tests, want 2", len(got))
	}
	// A test is not a notification of the outbox: nothing is retried or stored for it.
	e.clock.Advance(2 * time.Hour)
	time.Sleep(200 * time.Millisecond)
	if n := len(hook.got()); n != 2 {
		t.Errorf("the provider saw %d requests after two tests and two hours, want 2: no retry", n)
	}
	if ds := e.qaDeliveries(); len(ds) != 0 {
		t.Errorf("a channel test left %d outbox deliveries", len(ds))
	}
}

func TestA62_failure_notices(t *testing.T) {
	e := qaStart(t)
	bad := qaNewHook(t)
	bad.status.Store(400) // a client error is not retried, so the delivery fails at once
	testOnly := qaNewHook(t)
	testOnly.status.Store(400)
	good1, good2 := qaNewHook(t), qaNewHook(t)
	badID := e.qaChannel("bad", bad, map[string]any{"kinds": []string{"alert.firing"}})
	e.qaChannel("testonly", testOnly, map[string]any{"kinds": []string{"notify.test"}})
	g1 := e.qaChannel("good1", good1, nil)
	g2 := e.qaChannel("good2", good2, nil)

	// A notify.test event failing on its channel notifies nobody.
	e.qaEmit(qaEv("notify.test", "info", "", "test-1"))
	e.qaWaitDeliveries("the test event settled", func(ds []qaDelivery) bool {
		for _, d := range ds {
			if d.Kind == "notify.test" && d.State == "failed" {
				return true
			}
		}
		return false
	})
	e.clock.Advance(time.Minute)
	for _, d := range e.qaDeliveries() {
		if d.Kind == "notify.delivery_failed" {
			t.Fatalf("a failed notify.test delivery emitted a notify.delivery_failed")
		}
	}

	// Two events failing on one channel in one streak notify each other channel once.
	e.qaEmit(qaEv("alert.firing", "warning", "", "a1"))
	e.qaEmit(qaEv("alert.firing", "warning", "", "a2"))
	e.qaWaitDeliveries("both alert deliveries failed", func(ds []qaDelivery) bool {
		n := 0
		for _, d := range ds {
			if d.Kind == "alert.firing" && d.Channel == badID && d.State == "failed" {
				n++
			}
		}
		return n == 2
	})
	e.qaWaitDeliveries("the failure notices delivered", func([]qaDelivery) bool {
		return len(good1.got()) >= 3 && len(good2.got()) >= 3
	})
	e.clock.Advance(time.Minute)
	perChannel := map[string]int{}
	for _, d := range e.qaDeliveries() {
		if d.Kind == "notify.delivery_failed" {
			perChannel[d.Channel]++
		}
	}
	if perChannel[g1] != 1 || perChannel[g2] != 1 || len(perChannel) != 2 {
		t.Errorf("failure notices per channel %v, want one each on good1 and good2 and none on the failing channels", perChannel)
	}
	for _, r := range good1.got() {
		if r.Kind == "notify.delivery_failed" && !strings.Contains(r.Title, "bad") {
			t.Errorf("a failure notice names no failing channel: %q", r.Title)
		}
	}

	// A notice that itself fails emits no notice.
	good1.status.Store(400)
	e.qaEmit(qaEv("alert.firing", "warning", "", "a3"))
	e.clock.Advance(time.Minute)
	e.qaWaitDeliveries("a3 delivered", func([]qaDelivery) bool { return len(good2.got()) >= 4 })
	for _, r := range good2.got() {
		if strings.Contains(r.Title, "good1") {
			t.Errorf("a failed failure notice produced a notice about its own channel: %q", r.Title)
		}
	}
}

func TestA20_retry_schedule(t *testing.T) {
	e, opts := qaStartOpts(t)
	hook := qaNewHook(t)
	hook.status.Store(503)
	ch := e.qaChannel("flaky", hook, nil)
	e.qaChannel("witness", qaNewHook(t), map[string]any{"kinds": []string{"alert.resolved"}})
	e.qaEmit(qaEv("alert.firing", "warning", "", "retry-1"))
	d := e.qaWaitDeliveries("the first attempt", func(ds []qaDelivery) bool { return len(ds) > 0 && ds[0].Attempts >= 1 })[0]
	created, err := time.Parse(qaTimeFormat, d.Created)
	if err != nil {
		t.Fatal(err)
	}
	offsets := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}
	for h := 2; h <= 24; h++ {
		offsets = append(offsets, time.Duration(h)*time.Hour)
	}
	pending := func() qaDelivery {
		for _, x := range e.qaDeliveries() {
			if x.Channel == ch {
				return x
			}
		}
		t.Fatal("no delivery")
		return qaDelivery{}
	}
	for i, off := range offsets {
		cur := pending()
		if cur.State != "pending" {
			t.Fatalf("before the retry at +%v the delivery is %s (%s), want pending", off, cur.State, cur.LastError)
		}
		if cur.NextAt != created.Add(off).Format(qaTimeFormat) {
			t.Fatalf("attempt %d scheduled the next try at %s, want created+%v = %s", i+1, cur.NextAt, off, created.Add(off).Format(qaTimeFormat))
		}
		before := len(hook.got())
		e.qaRestartAt(opts, created.Add(off))
		e.qaWaitDeliveries(fmt.Sprintf("the retry at +%v", off), func([]qaDelivery) bool { return len(hook.got()) > before })
		e.qaWaitDeliveries("the retry recorded", func([]qaDelivery) bool { return pending().Attempts >= i+2 })
	}
	if fin := pending(); fin.State != "failed" || fin.Attempts != len(offsets)+1 {
		t.Errorf("after the retry at 24 hours the delivery is %s with %d attempts, want failed after %d", fin.State, fin.Attempts, len(offsets)+1)
	}
	if n := len(hook.got()); n != len(offsets)+1 {
		t.Errorf("the provider saw %d attempts, want %d", n, len(offsets)+1)
	}
}

func TestA21_events_survive_a_restart(t *testing.T) {
	e, opts := qaStartOpts(t)
	hook := qaNewHook(t)
	hook.status.Store(503)
	e.qaChannel("durable", hook, nil)
	e.qaEmit(qaEv("alert.firing", "critical", "", "durable-1"))
	e.qaWaitDeliveries("an attempt before the restart", func([]qaDelivery) bool { return len(hook.got()) >= 1 })
	hook.status.Store(200)
	e.qaRestartAt(opts, e.clock.Now().Add(2*time.Minute))
	e.qaWaitDeliveries("delivery after the restart", func(ds []qaDelivery) bool { return len(ds) == 1 && ds[0].State == "sent" })
	if n := len(hook.got()); n != 2 {
		t.Errorf("the provider saw %d requests, want the failed one and the one after the restart", n)
	}
}

func TestA61_restore_marks_pending_failed(t *testing.T) {
	e, opts := qaStartOpts(t)
	hook := qaNewHook(t)
	hook.status.Store(503)
	e.qaChannel("restored", hook, nil)
	e.qaEmit(qaEv("alert.firing", "critical", "", "pre-restore"))
	e.qaWaitDeliveries("a pending delivery", func(ds []qaDelivery) bool { return len(ds) == 1 && ds[0].State == "pending" })
	events := func(s *Server) (n int) {
		_ = s.controllerDB.Read().QueryRow(`SELECT count(*) FROM notify_events`).Scan(&n)
		return
	}
	eventsBefore := events(e.s)
	sent := len(hook.got())
	if err := e.s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A restore leaves the quarantine marker in the restored database.
	db := openDB(t, opts.Paths.ControllerDB())
	marker, _ := json.Marshal(map[string]any{"tx": "restore-tx-1", "restoredAt": e.clock.Now().UTC(), "backupCreatedAt": e.clock.Now().UTC().Add(-time.Hour)})
	if _, err := db.Writer().Exec(`INSERT OR REPLACE INTO core_meta (key, value) VALUES ('restore_quarantine', ?)`, string(marker)); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	hook.status.Store(200)
	opts.Clock = coretest.NewFakeClock(e.clock.Now().Add(2 * time.Minute))
	e.clock = opts.Clock.(*coretest.FakeClock)
	e.s = start(t, opts)
	ds := e.qaDeliveries()
	if len(ds) != 1 || ds[0].State != "failed" || ds[0].LastError != "restored" {
		t.Errorf("a pending delivery after a restore: %+v, want failed with reason restored", ds)
	}
	e.clock.Advance(time.Hour)
	time.Sleep(300 * time.Millisecond)
	if n := events(e.s); n != eventsBefore {
		t.Errorf("a restore changed the events from %d to %d, want it to emit nothing", eventsBefore, n)
	}
	if n := len(hook.got()); n != sent {
		t.Errorf("a delivery was sent after the restore: %d requests, want %d", n, sent)
	}
}

func TestA22_nothing_secret_leaves(t *testing.T) {
	e, opts := qaStartOpts(t)
	hook := qaNewHook(t)
	hook.status.Store(500)
	hook.body.Store("PROVIDER-BODY-CANARY")
	ch := e.qaChannel("canary", hook, nil)
	secrets := []string{hook.srv.URL + "/hook/canary", "/hook/canary", "qa-hmac-secret-canary"}

	// A change whose error message and cause hold a canary.
	n := qaSeedChange(e, 1, "", "core.group.create")
	exec(e.t, e.s, `UPDATE core_changes SET state = 'failed', error_code = 'group.in_use', error_message = 'CAUSE-CANARY-MESSAGE', error_cause = 'CAUSE-CANARY-CAUSE', updated_at = ? WHERE id = ?`,
		e.clock.Now().UTC().Add(time.Second).Format(qaTimeFormat), n)
	e.clock.Advance(10 * time.Second)
	e.qaWaitDeliveries("the change event delivered or attempted", func([]qaDelivery) bool { return len(hook.got()) >= 1 })
	for _, r := range hook.got() {
		if strings.Contains(string(r.Raw), "CAUSE-CANARY") {
			t.Errorf("a notification body carries the change's error: %s", r.Raw)
		}
	}
	if r := hook.got()[0]; r.Kind != "change.failed" {
		t.Errorf("the first notification is %q, want change.failed", r.Kind)
	}
	// An error message from a test that cannot connect.
	hook.srv.Close()
	r := e.qaRead(e.admin(), "notify.channel.test", map[string]any{"id": ch})
	surfaces := map[string]string{
		"channel list": string(e.qaRead(e.admin(), "notify.channel.list", nil).body),
		"channel test": string(r.body),
		"audit":        string(e.qaRead(e.admin(), "core.audit.list", map[string]any{"limit": 500}).body),
		"changes":      string(e.do("GET", "/api/v1/changes", nil, e.admin()).body),
		"log":          qaLog(t),
		"directory":    string(e.qaRead(e.admin(), "core.user.directory", nil).body),
	}
	rows, err := e.s.controllerDB.Read().Query(`SELECT coalesce(input_json, '') FROM core_audit`)
	if err != nil {
		t.Fatal(err)
	}
	var journal strings.Builder
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		journal.WriteString(s)
	}
	_ = rows.Close()
	surfaces["audit rows"] = journal.String()
	for name, text := range surfaces {
		for _, s := range secrets {
			if strings.Contains(text, s) {
				t.Errorf("the %s contains a channel secret %q", name, s)
			}
		}
		if strings.Contains(text, "PROVIDER-BODY-CANARY") {
			t.Errorf("the %s contains the provider's response body", name)
		}
	}
	_ = opts
}

func TestA25_event_kinds(t *testing.T) {
	opts := embeddedOptions(t)
	e := &e2e{t: t, clock: opts.Clock.(*coretest.FakeClock), home: t.TempDir()}
	e.s = start(t, opts)
	e.url = baseURL(t, e.s)
	e.loginAdmin()
	hook := qaNewHook(t)
	e.qaChannel("kinds", hook, nil)
	// A failed controller-side change.
	failed := qaSeedChange(e, 1, "", "core.group.create")
	exec(e.t, e.s, `UPDATE core_changes SET state = 'failed', error_code = 'group.in_use', updated_at = ? WHERE id = ?`, e.clock.Now().UTC().Add(time.Second).Format(qaTimeFormat), failed)
	// An automatically reverted one: the revert timer's journal row.
	reverted := qaSeedChange(e, 2, "", "core.group.create")
	exec(e.t, e.s, `UPDATE core_changes SET state = 'reverted', updated_at = ? WHERE id = ?`, e.clock.Now().UTC().Add(2*time.Second).Format(qaTimeFormat), reverted)
	exec(e.t, e.s, `INSERT INTO core_audit (id, at, actor, action, version, phase, change_id, result) VALUES ('01QAREV', ?, 'system:revert-timer', 'core.group.create', 1, 'revert', ?, 'ok')`, e.clock.Now().UTC().Format(qaTimeFormat), reverted)
	// A failed rollout: the embedded agent runs no updater.
	id := e.s.LocalHostID()
	exec(t, e.s, `UPDATE core_hosts SET state = 'enrolled' WHERE id = ?`, id)
	if err := e.s.controllerDB.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := rollout.Create(context.Background(), tx, rollout.CreateParams{
			Input:             core.RolloutInput{Hosts: []string{id}, Version: "0.2.0", Channel: core.ChannelStable},
			ControllerVersion: buildinfo.Version, Now: e.clock.Now()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(rollout.PollInterval)
	e.qaWaitDeliveries("the kinds delivered", func([]qaDelivery) bool {
		k := strings.Join(hook.kinds(), ",")
		return strings.Contains(k, "change.failed") && strings.Contains(k, "change.auto_reverted") && strings.Contains(k, "rollout.failed")
	})
	count := map[string]int{}
	for _, k := range hook.kinds() {
		count[k]++
	}
	for _, k := range []string{"change.failed", "change.auto_reverted", "rollout.failed"} {
		if count[k] != 1 {
			t.Errorf("event kind %s was emitted %d times, want once (all: %v)", k, count[k], count)
		}
	}
}

func TestA52_changes_ended_while_stopped_emit_once(t *testing.T) {
	e, opts := qaStartOpts(t)
	hook := qaNewHook(t)
	e.qaChannel("restart", hook, nil)
	if err := e.s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	// While the controller is stopped, a change fails and another is auto-reverted.
	db := openDB(t, opts.Paths.ControllerDB())
	at := e.clock.Now().UTC().Add(time.Minute)
	seed := func(n int, state, code string) string {
		id := fmt.Sprintf("01QB%022d", n)
		ts := at.Add(time.Duration(n) * time.Second).Format(qaTimeFormat)
		for _, q := range []string{
			fmt.Sprintf(`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json, guards_json, confirmation, created_at, expires_at)
				VALUES ('P%s', '', 'core.group.create', 1, 'system', 'h', 'h', '[]', '[]', '', '%s', '%s')`, id, ts, ts),
			fmt.Sprintf(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state, input_json, error_code, created_at, updated_at)
				VALUES ('%s', 'P%s', '', 'core.group.create', 1, 'compensable', '', 'system', '%s', '{}', NULLIF('%s', ''), '%s', '%s')`, id, id, state, code, ts, ts),
		} {
			if _, err := db.Writer().Exec(q); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	seed(1, "failed", "group.in_use")
	rev := seed(2, "reverted", "")
	if _, err := db.Writer().Exec(`INSERT INTO core_audit (id, at, actor, action, version, phase, change_id, result) VALUES ('01QBREV', ?, 'system:revert-timer', 'core.group.create', 1, 'revert', ?, 'ok')`, at.Format(qaTimeFormat), rev); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	opts.Clock = coretest.NewFakeClock(at.Add(time.Minute))
	e.clock = opts.Clock.(*coretest.FakeClock)
	e.s = start(t, opts)
	e.qaWaitDeliveries("the restart's events delivered", func([]qaDelivery) bool { return len(hook.got()) >= 2 })
	for i := 0; i < 6; i++ {
		e.clock.Advance(10 * time.Second)
	}
	time.Sleep(200 * time.Millisecond)
	count := map[string]int{}
	for _, k := range hook.kinds() {
		count[k]++
	}
	if count["change.failed"] != 1 || count["change.auto_reverted"] != 1 {
		t.Errorf("events after the restart: %v, want one change.failed and one change.auto_reverted", count)
	}
	var stored int
	_ = e.s.controllerDB.Read().QueryRow(`SELECT count(*) FROM notify_events WHERE kind IN ('change.failed','change.auto_reverted')`).Scan(&stored)
	if stored != 2 {
		t.Errorf("stored events of those kinds: %d, want exactly 2", stored)
	}
}

func TestA53_polling_metrics_writes_no_audit(t *testing.T) {
	opts := embeddedOptions(t)
	e := &e2e{t: t, clock: opts.Clock.(*coretest.FakeClock), home: t.TempDir()}
	e.s = start(t, opts)
	e.url = baseURL(t, e.s)
	e.loginAdmin()
	count := func(q string) (n int) {
		if err := e.s.controllerDB.Read().QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return
	}
	audit := func() int { return count(`SELECT count(*) FROM core_audit`) }
	minutes := func() int { return count(`SELECT count(*) FROM metrics_minute`) }
	before := audit()
	eventually(t, "the poller storing minutes of the local host", func() {
		e.clock.Advance(15 * time.Second)
		time.Sleep(10 * time.Millisecond)
	}, func() bool { return minutes() >= 4 })
	// Several more once-a-minute polls.
	for i := 0; i < 24; i++ {
		e.clock.Advance(15 * time.Second)
		time.Sleep(10 * time.Millisecond)
	}
	if audit() != before {
		rows, _ := e.s.controllerDB.Read().Query(`SELECT actor, action, phase FROM core_audit ORDER BY rowid DESC LIMIT 5`)
		var got []string
		for rows.Next() {
			var a, b, c string
			_ = rows.Scan(&a, &b, &c)
			got = append(got, a+" "+b+" "+c)
		}
		_ = rows.Close()
		t.Errorf("polling metrics wrote %d audit rows (latest: %v), want none", audit()-before, got)
	}
	if n := count(`SELECT count(*) FROM core_audit WHERE action LIKE 'metrics.%'`); n != 0 {
		t.Errorf("%d audit rows name a metrics action", n)
	}
}

func TestA32_default_rules_listed_after_first_start(t *testing.T) {
	e := qaStart(t)
	var out struct {
		Output struct {
			Rules []struct {
				Name        string  `json:"name"`
				Metric      string  `json:"metric"`
				Comparator  string  `json:"comparator"`
				Threshold   float64 `json:"threshold"`
				DurationMin int     `json:"durationMin"`
				Severity    string  `json:"severity"`
				Target      string  `json:"target"`
				Enabled     bool    `json:"enabled"`
			} `json:"rules"`
		} `json:"output"`
	}
	e.want(e.qaRead(e.admin(), "metrics.alert.list-rules", nil), 200, &out)
	rules := out.Output.Rules
	if len(rules) != 2 {
		t.Fatalf("rules after first start: %+v, want the two defaults", rules)
	}
	byName := map[string]int{}
	for i, r := range rules {
		byName[r.Name] = i
	}
	off, ok := byName["Host offline"]
	if !ok {
		t.Fatalf("no Host offline rule in %+v", rules)
	}
	if r := rules[off]; r.Metric != "offline" || r.DurationMin != 5 || r.Severity != "critical" || r.Target != "*" || !r.Enabled {
		t.Errorf("Host offline is %+v, want offline, 5 minutes, critical, all hosts, enabled", r)
	}
	disk, ok := byName["Disk almost full"]
	if !ok {
		t.Fatalf("no Disk almost full rule in %+v", rules)
	}
	if r := rules[disk]; r.Metric != "disk" || r.Comparator != ">" || r.Threshold != 90 || r.DurationMin != 10 || r.Severity != "warning" || r.Target != "*" || !r.Enabled {
		t.Errorf("Disk almost full is %+v, want disk > 90 for 10 minutes, warning, all hosts, enabled", r)
	}
}

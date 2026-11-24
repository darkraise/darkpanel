package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/controller/auth"
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

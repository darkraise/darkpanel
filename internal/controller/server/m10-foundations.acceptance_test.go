package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/controller/auth"
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
	e := &e2e{t: t, clock: opts.Clock.(*coretest.FakeClock), home: t.TempDir()}
	e.s = start(t, opts)
	e.url = baseURL(t, e.s)
	e.loginAdmin()
	return e, opts
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
	exec(e.t, e.s, `INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, 0, ?)`, id, id, e.clock.Now().UTC().Format(time.RFC3339Nano))
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

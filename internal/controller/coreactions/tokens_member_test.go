package coreactions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type probeWebCA struct{}

func (probeWebCA) RootPEM() []byte {
	return []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n")
}
func (probeWebCA) Fingerprint() string { return strings.Repeat("ab", 32) }

type probeRead struct{}

func (probeRead) Read(context.Context, core.Env, NoInput) (Empty, error) { return Empty{}, nil }

type probeSet struct{}

func (probeSet) Plan(context.Context, core.Env, NoInput) (core.PlanOutcome, error) {
	return core.PlanOutcome{Guards: []core.Guard{}}, nil
}
func (probeSet) Apply(context.Context, core.Env, NoInput) (Empty, error) { return Empty{}, nil }
func (probeSet) Verify(context.Context, core.Env, NoInput, Empty) error  { return nil }

// tokenAPI serves the real API over e's engine for controller-side actions;
// agent-side ones answer from a stub that records each host it reached.
type tokenAPI struct {
	t       *testing.T
	srv     *api.Server
	reached []string
}

func newTokenAPI(t *testing.T) (*testEnv, *tokenAPI) {
	t.Helper()
	e := newTestEnvWith(t, func(_ *testEnv, d *Deps) {
		d.Extra = []core.ActionDef{
			core.DefineRead[NoInput, Empty](core.ActionSpec{Name: "core.probe.get", Version: 1, Side: core.SideAgent,
				Summary: "Read a probe."}, probeRead{}),
			core.DefineMutating[NoInput, Empty](core.ActionSpec{Name: "core.probe.set", Version: 1, Side: core.SideAgent,
				Class: core.ClassIrreversible, Summary: "Set a probe."}, probeSet{}),
		}
	})
	a := &tokenAPI{t: t}
	disp := &coretest.FakeDispatcher{
		ReadFunc: func(ctx context.Context, p core.Principal, host string, ref core.ActionRef, in json.RawMessage) (json.RawMessage, error) {
			if host != "" {
				a.reached = append(a.reached, "read "+host)
				return json.RawMessage(`{}`), nil
			}
			return e.engine.Read(ctx, p, ref, in)
		},
		PlanFunc: func(ctx context.Context, p core.Principal, host string, ref core.ActionRef, in json.RawMessage, o core.PlanOptions) (core.PlanView, error) {
			if host != "" {
				a.reached = append(a.reached, "plan "+host)
				return core.PlanView{}, errcode.New(errcode.Internal, "agent-side plans are not served here")
			}
			return e.engine.Plan(ctx, p, ref, in, o)
		},
		ApplyFunc: func(ctx context.Context, p core.Principal, _ string, _ core.ActionRef, planID string, o core.ApplyOptions) (core.ChangeView, error) {
			return e.engine.Apply(ctx, p, planID, o)
		},
	}
	srv, err := api.New(api.Config{Dispatcher: disp, Auth: e.auth, Registry: e.reg, WebCA: probeWebCA{},
		SchemaVersion: func(context.Context) (int, error) { return 3, nil }, HostID: "01HOST0000000000000000000A",
		UI: fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>Darkpanel</title>")}}})
	if err != nil {
		t.Fatal(err)
	}
	a.srv = srv
	return e, a
}

// caller is one set of credentials: a session with its CSRF token, or a
// bearer token.
type caller struct{ session, csrf, token string }

const tokenRemote = "192.0.2.10"

// call posts body to path as c and returns the status and the decoded body.
func (a *tokenAPI) call(c caller, path, body string) (int, map[string]any) {
	a.t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.RemoteAddr = tokenRemote + ":51234"
	r.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	} else {
		r.Header.Add("Cookie", api.SessionCookiePlain+"="+c.session)
		r.Header.Set(api.CSRFHeader, c.csrf)
	}
	rec := httptest.NewRecorder()
	a.srv.Handler().ServeHTTP(rec, r)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		a.t.Fatalf("POST %s: %d %s", path, rec.Code, rec.Body)
	}
	return rec.Code, out
}

// refused fails unless POST path answered status with code.
func (a *tokenAPI) refused(c caller, path, body string, status int, code errcode.Code) {
	a.t.Helper()
	got, out := a.call(c, path, body)
	if got != status || out["code"] != string(code) {
		a.t.Errorf("POST %s %s: %d %v; want %d %s", path, body, got, out, status, code)
	}
}

// runAction plans and applies name as c, repeating the plan's confirmation,
// and returns the change's output.
func (a *tokenAPI) runAction(c caller, name, input string) map[string]any {
	a.t.Helper()
	base := "/api/v1/actions/" + name + "/v1/"
	status, plan := a.call(c, base+"plan", `{"input":`+input+`}`)
	if status != http.StatusOK {
		a.t.Fatalf("plan %s %s: %d %v", name, input, status, plan)
	}
	confirmation, _ := plan["confirmation"].(string)
	body, _ := json.Marshal(map[string]any{"planId": plan["planId"], "confirmation": confirmation})
	status, change := a.call(c, base+"apply", string(body))
	if status != http.StatusOK || change["state"] != core.StateCompleted {
		a.t.Fatalf("apply %s %s: %d %v", name, input, status, change)
	}
	out, _ := change["output"].(map[string]any)
	return out
}

// listTokens returns the ids of c's listed tokens.
func (a *tokenAPI) listTokens(c caller) []string {
	a.t.Helper()
	status, res := a.call(c, "/api/v1/actions/core.token.list/v1/read", `{"input":{}}`)
	if status != http.StatusOK {
		a.t.Fatalf("core.token.list: %d %v", status, res)
	}
	out, _ := res["output"].(map[string]any)
	tokens, _ := out["tokens"].([]any)
	ids := []string{}
	for _, tok := range tokens {
		ids = append(ids, tok.(map[string]any)["id"].(string))
	}
	slices.Sort(ids)
	return ids
}

func TestMembersManageOwnTokens(t *testing.T) {
	e, a := newTokenAPI(t)
	ctx := context.Background()
	e.addHost("h1", "web-1", false)
	e.addHost("h2", "db-1", false)
	e.exec(`INSERT INTO core_host_groups (id, name, created_at) VALUES ('G', 'web', '2026-11-24T09:00:00.000000000Z')`)
	e.exec(`INSERT INTO core_host_group_members (group_id, host_id) VALUES ('G', 'h1')`)
	rootUser, _ := e.activeUser("root")
	rootInfo, _, err := e.auth.CreateToken(ctx, rootUser.ID, "root-ci", core.ScopeAdmin, 0)
	if err != nil {
		t.Fatal(err)
	}

	_, secret, err := e.auth.CreateUserWithRole(ctx, "mia", testPassword, auth.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TOTPCode(secret, e.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	mia, err := e.auth.ActivateUser(ctx, "mia", code)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.SetGrants(ctx, "mia", []auth.Grant{{Target: "G", Role: auth.GrantViewer}}); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(auth.TOTPPeriod)
	if code, err = auth.TOTPCode(secret, e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	login, err := e.auth.Login(ctx, auth.LoginRequest{Username: "mia", Password: testPassword, Code: code, Source: tokenRemote})
	if err != nil {
		t.Fatal(err)
	}
	session := caller{session: login.SessionID, csrf: login.CSRFToken}

	readTok := a.runAction(session, "core.token.create", `{"name":"laptop","scope":"read"}`)
	opTok := a.runAction(session, "core.token.create", `{"name":"ci","scope":"operate"}`)
	for _, tok := range []map[string]any{readTok, opTok} {
		if tok["token"] == "" || tok["token"] == nil {
			t.Fatalf("create output %v carries no token", tok)
		}
	}
	a.refused(session, "/api/v1/actions/core.token.create/v1/plan", `{"input":{"name":"root","scope":"admin"}}`,
		http.StatusForbidden, errcode.Forbidden)

	want := []string{readTok["id"].(string), opTok["id"].(string)}
	slices.Sort(want)
	if got := a.listTokens(session); !slices.Equal(got, want) {
		t.Errorf("mia's list %v; want only her tokens %v", got, want)
	}
	if got := a.listTokens(caller{token: readTok["token"].(string)}); !slices.Equal(got, want) {
		t.Errorf("mia's list through her read token %v; want %v", got, want)
	}

	for _, tok := range []map[string]any{readTok, opTok} {
		c := caller{token: tok["token"].(string)}
		a.refused(c, "/api/v1/actions/core.token.create/v1/plan", `{"input":{"name":"more","scope":"read"}}`,
			http.StatusForbidden, errcode.Forbidden)
		a.refused(c, "/api/v1/actions/core.token.revoke/v1/plan", fmt.Sprintf(`{"input":{"id":%q}}`, readTok["id"]),
			http.StatusForbidden, errcode.Forbidden)
	}
	a.refused(session, "/api/v1/actions/core.token.revoke/v1/plan", fmt.Sprintf(`{"input":{"id":%q}}`, rootInfo.ID),
		http.StatusNotFound, errcode.NotFound)

	// The operate token on a viewer grant reads h1 but cannot change it,
	// and h2 stays unknown.
	op := caller{token: opTok["token"].(string)}
	if status, res := a.call(op, "/api/v1/hosts/h1/actions/core.probe.get/v1/read", `{"input":{}}`); status != http.StatusOK {
		t.Errorf("operate token read on h1: %d %v; want 200", status, res)
	}
	a.refused(op, "/api/v1/hosts/h1/actions/core.probe.set/v1/plan", `{"input":{}}`, http.StatusForbidden, errcode.Forbidden)
	a.refused(op, "/api/v1/hosts/h2/actions/core.probe.get/v1/read", `{"input":{}}`, http.StatusNotFound, errcode.NotFound)
	if !slices.Equal(a.reached, []string{"read h1"}) {
		t.Errorf("agent-side calls dispatched %v; want only the read on h1", a.reached)
	}

	revoked := a.runAction(session, "core.token.revoke", fmt.Sprintf(`{"id":%q}`, readTok["id"]))
	if revoked["revokedAt"] == nil {
		t.Errorf("revoke output %v; want the token revoked", revoked)
	}
	if info, err := e.auth.GetToken(ctx, rootUser.ID, rootInfo.ID); err != nil || info.RevokedAt != nil {
		t.Errorf("root's token after mia's revokes: %+v, %v; want it active", info, err)
	}
	if info, err := e.auth.GetToken(ctx, mia.ID, readTok["id"].(string)); err != nil || info.RevokedAt == nil {
		t.Errorf("mia's read token after its revoke: %+v, %v; want it revoked", info, err)
	}
}

// TestTokenActionsRefuseAMemberOutsideTheAPI: the handlers themselves
// refuse a member's admin token and a member's token as the caller.
func TestTokenActionsRefuseAMemberOutsideTheAPI(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	e.activeUser("root")
	u, _ := e.activeUser("mia")
	if _, err := e.auth.SetRole(ctx, "mia", auth.RoleMember); err != nil {
		t.Fatal(err)
	}
	session := core.Principal{Actor: "user:" + u.ID, UserID: u.ID, Source: "127.0.0.1", Access: core.Access{All: core.ScopeRead}}
	_, err := e.plan(session, "core.token.create", TokenCreateInput{Name: "x", Scope: core.ScopeAdmin})
	wantCode(t, err, errcode.Forbidden)
	pv, err := e.plan(session, "core.token.create", TokenCreateInput{Name: "x", Scope: core.ScopeOperate})
	if err != nil {
		t.Fatal(err)
	}
	if d := diffText(pv.Diff); !strings.Contains(d, "it can do no more than mia's grants allow") {
		t.Errorf("member create diff %q; want the grants cap named", d)
	}
	if _, err := e.apply(session, pv.PlanID, pv.Confirmation); err != nil {
		t.Fatal(err)
	}

	info, _, err := e.auth.CreateToken(ctx, u.ID, "y", core.ScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}
	token := session
	token.Actor, token.TokenID = "token:"+info.ID, info.ID
	_, err = e.plan(token, "core.token.create", TokenCreateInput{Name: "z", Scope: core.ScopeRead})
	wantCode(t, err, errcode.Forbidden)
	_, err = e.plan(token, "core.token.revoke", TokenRevokeInput{ID: info.ID})
	wantCode(t, err, errcode.Forbidden)
}

// TestTokenCreateApplyRechecksTheScope: an admin-scope token planned by an
// admin who is a member by the time the plan is applied is refused when it
// is applied, and no token is created.
func TestTokenCreateApplyRechecksTheScope(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	e.activeUser("root")
	u, admin := e.activeUser("mia")
	pv, err := e.plan(admin, "core.token.create", TokenCreateInput{Name: "x", Scope: core.ScopeAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.SetRole(ctx, "mia", auth.RoleMember); err != nil {
		t.Fatal(err)
	}
	member := core.Principal{Actor: admin.Actor, UserID: u.ID, Source: admin.Source, Access: core.Access{All: core.ScopeRead}}
	_, err = e.apply(member, pv.PlanID, pv.Confirmation)
	wantCode(t, err, errcode.Forbidden)
	tokens, err := e.auth.ListTokens(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Errorf("mia's tokens after the refused apply: %+v; want none", tokens)
	}
}

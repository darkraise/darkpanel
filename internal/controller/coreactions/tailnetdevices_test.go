package coreactions

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// deviceEnv serves the real API over an admin root with an admin token, a
// member mia with a viewer grant, her session and her read and operate
// tokens, and an admin ben.
type deviceEnv struct {
	*testEnv
	a                       *tokenAPI
	root, session, read, op caller
	rootP                   core.Principal
	miaID, benID            string
}

func newDeviceEnv(t *testing.T) *deviceEnv {
	t.Helper()
	e, a := newTokenAPI(t)
	e.addHost("h1", "web-1", false)
	e.exec(`INSERT INTO core_host_groups (id, name, created_at) VALUES ('G', 'web', '2026-11-24T09:00:00.000000000Z')`)
	e.exec(`INSERT INTO core_host_group_members (group_id, host_id) VALUES ('G', 'h1')`)
	rootUser, rootP := e.activeUser("root")
	ben, _ := e.activeUser("ben")
	d := &deviceEnv{testEnv: e, a: a, rootP: rootP, benID: ben.ID}
	d.root = d.token(rootUser.ID, core.ScopeAdmin)
	d.miaID, d.session = d.member("mia", auth.Grant{Target: "G", Role: auth.GrantViewer})
	d.read, d.op = d.token(d.miaID, core.ScopeRead), d.token(d.miaID, core.ScopeOperate)
	return d
}

// member adds an active member username with grants and returns its id and
// a signed-in session.
func (e *deviceEnv) member(username string, grants ...auth.Grant) (string, caller) {
	e.t.Helper()
	ctx := context.Background()
	_, secret, err := e.auth.CreateUserWithRole(ctx, username, testPassword, auth.RoleMember)
	if err != nil {
		e.t.Fatal(err)
	}
	code, err := auth.TOTPCode(secret, e.clock.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	u, err := e.auth.ActivateUser(ctx, username, code)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(grants) > 0 {
		if _, err := e.auth.SetGrants(ctx, username, grants); err != nil {
			e.t.Fatal(err)
		}
	}
	e.clock.Advance(auth.TOTPPeriod)
	if code, err = auth.TOTPCode(secret, e.clock.Now()); err != nil {
		e.t.Fatal(err)
	}
	login, err := e.auth.Login(ctx, auth.LoginRequest{Username: username, Password: testPassword, Code: code, Source: tokenRemote})
	if err != nil {
		e.t.Fatal(err)
	}
	return u.ID, caller{session: login.SessionID, csrf: login.CSRFToken}
}

func (e *deviceEnv) token(userID string, scope core.Scope) caller {
	e.t.Helper()
	_, tok, err := e.auth.CreateToken(context.Background(), userID, string(scope), scope, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	return caller{token: tok}
}

// remember inserts userID's device stableID, last used at minute used.
func (e *deviceEnv) remember(userID, stableID, nodeName, used string) {
	e.t.Helper()
	at := "2026-11-25T10:" + used + ":00.000000000Z"
	e.exec(`INSERT INTO core_user_tailnet_devices (user_id, stable_id, node_name, remembered_at, last_used_at)
		VALUES (?, ?, ?, ?, ?)`, userID, stableID, nodeName, at, at)
}

// devices is userID's stable ids, sorted.
func (e *deviceEnv) devices(userID string) string {
	e.t.Helper()
	rows, err := e.db.Read().Query(`SELECT stable_id FROM core_user_tailnet_devices WHERE user_id = ? ORDER BY stable_id`, userID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			e.t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return strings.Join(ids, ",")
}

// list reads action as c and returns the listed stable ids in order.
func (e *deviceEnv) list(c caller, action, input string) string {
	e.t.Helper()
	status, res := e.a.call(c, "/api/v1/actions/"+action+"/v1/read", `{"input":`+input+`}`)
	if status != http.StatusOK {
		e.t.Fatalf("%s: %d %v", action, status, res)
	}
	out, _ := res["output"].(map[string]any)
	devices, ok := out["devices"].([]any)
	if !ok {
		e.t.Fatalf("%s output %v carries no devices list", action, out)
	}
	ids := []string{}
	for _, d := range devices {
		ids = append(ids, d.(map[string]any)["stableId"].(string))
	}
	return strings.Join(ids, ",")
}

func TestTailnetDeviceListOwn(t *testing.T) {
	e := newDeviceEnv(t)
	e.remember(e.miaID, "nB", "phone", "01")
	e.remember(e.miaID, "nL", "laptop", "05")
	e.remember(e.benID, "nX", "ben-laptop", "09")
	for name, c := range map[string]caller{"session": e.session, "read token": e.read} {
		if got := e.list(c, "core.tailnetdevice.list", `{}`); got != "nL,nB" {
			t.Errorf("mia's list through her %s: %q, want only her nL then nB", name, got)
		}
	}
	if got := e.list(e.root, "core.tailnetdevice.list", `{}`); got != "" {
		t.Errorf("root's list: %q, want none", got)
	}
	// A member without any grant still lists their own devices.
	zoeID, zoe := e.member("zoe")
	e.remember(zoeID, "nZ", "zoe-laptop", "02")
	if got := e.list(zoe, "core.tailnetdevice.list", `{}`); got != "nZ" {
		t.Errorf("zoe's list without grants: %q, want nZ", got)
	}
	if got := e.list(e.token(zoeID, core.ScopeRead), "core.tailnetdevice.list", `{}`); got != "nZ" {
		t.Errorf("zoe's list through her read token: %q, want nZ", got)
	}
}

func TestTailnetDeviceForgetOwn(t *testing.T) {
	for _, tc := range []struct{ name, stableID, nodeName, wantDiff string }{
		{"a named device", "nL", "laptop", "laptop (nL)"},
		{"a device with an empty node name", "nE", "", "Unnamed device (nE)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newDeviceEnv(t)
			e.remember(e.miaID, tc.stableID, tc.nodeName, "01")
			e.remember(e.miaID, "nKeep", "phone", "02")
			status, plan := e.a.call(e.session, "/api/v1/actions/core.tailnetdevice.forget/v1/plan",
				fmt.Sprintf(`{"input":{"stableId":%q}}`, tc.stableID))
			if status != http.StatusOK || plan["confirmation"] != tc.stableID || !strings.Contains(fmt.Sprint(plan["diff"]), tc.wantDiff) {
				t.Errorf("plan = %d %v; want phrase %s and a diff naming %q", status, plan, tc.stableID, tc.wantDiff)
			}
			out := e.a.runAction(e.session, "core.tailnetdevice.forget", fmt.Sprintf(`{"stableId":%q}`, tc.stableID))
			if out["stableId"] != tc.stableID || out["nodeName"] != tc.nodeName {
				t.Errorf("forget output %v, want the forgotten %s", out, tc.stableID)
			}
			if got := e.devices(e.miaID); got != "nKeep" {
				t.Errorf("mia's devices after the forget: %q, want only nKeep", got)
			}
		})
	}
}

func TestTailnetDeviceForgetOwnAnyGrants(t *testing.T) {
	e := newDeviceEnv(t)
	for _, tc := range []struct {
		username string
		grants   []auth.Grant
	}{
		{"nogrants", nil},
		{"operator", []auth.Grant{{Target: "*", Role: auth.GrantOperator}}},
	} {
		userID, c := e.member(tc.username, tc.grants...)
		e.remember(userID, "nM", "laptop", "03")
		e.a.runAction(c, "core.tailnetdevice.forget", `{"stableId":"nM"}`)
		if got := e.devices(userID); got != "" {
			t.Errorf("member %s: devices after the forget %q, want none", tc.username, got)
		}
	}
}

func TestTailnetDeviceForgetRefusals(t *testing.T) {
	const plan = "/api/v1/actions/core.tailnetdevice.forget/v1/plan"
	for _, tc := range []struct {
		name     string
		as       func(e *deviceEnv) caller
		stableID string
		status   int
		code     errcode.Code
		detail   string
	}{
		{"another user's device", func(e *deviceEnv) caller { return e.session }, "nX", http.StatusNotFound, errcode.NotFound, ""},
		{"a member read token", func(e *deviceEnv) caller { return e.read }, "nL", http.StatusForbidden, errcode.Forbidden,
			"this action needs a signed-in session, not a token"},
		{"a member operate token", func(e *deviceEnv) caller { return e.op }, "nL", http.StatusForbidden, errcode.Forbidden,
			"this action needs a signed-in session, not a token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newDeviceEnv(t)
			e.remember(e.miaID, "nL", "laptop", "01")
			e.remember(e.benID, "nX", "ben-laptop", "02")
			status, out := e.a.call(tc.as(e), plan, fmt.Sprintf(`{"input":{"stableId":%q}}`, tc.stableID))
			if status != tc.status || out["code"] != string(tc.code) || !strings.Contains(fmt.Sprint(out["detail"]), tc.detail) {
				t.Errorf("plan = %d %v; want %d %s %q", status, out, tc.status, tc.code, tc.detail)
			}
			if e.devices(e.miaID) != "nL" || e.devices(e.benID) != "nX" {
				t.Errorf("devices mia %q, ben %q; want both kept", e.devices(e.miaID), e.devices(e.benID))
			}
		})
	}
	// The handler refuses a member's token too, outside the API.
	e := newDeviceEnv(t)
	e.remember(e.miaID, "nL", "laptop", "01")
	member := core.Principal{Actor: "token:t", UserID: e.miaID, TokenID: "t", Source: "127.0.0.1", Access: core.Access{All: core.ScopeOperate}}
	_, err := e.plan(member, "core.tailnetdevice.forget", TailnetDeviceInput{StableID: "nL"})
	wantCode(t, err, errcode.Forbidden)
}

func TestUserTailnetDeviceAdmin(t *testing.T) {
	e := newDeviceEnv(t)
	e.remember(e.miaID, "nL", "laptop", "01")
	e.remember(e.miaID, "nP", "phone", "03")
	if got := e.list(e.root, "core.user.list-tailnet-devices", `{"username":"mia"}`); got != "nP,nL" {
		t.Fatalf("admin list of mia: %q, want nP, nL", got)
	}
	pv, err := e.plan(e.rootP, "core.user.forget-tailnet-device", UserTailnetDeviceInput{Username: "mia", StableID: "nL"})
	if err != nil {
		t.Fatal(err)
	}
	if d := diffText(pv.Diff); pv.Confirmation != "nL" || !strings.Contains(d, "laptop (nL) of user mia") {
		t.Errorf("plan = phrase %q, diff %q; want phrase nL naming mia's laptop", pv.Confirmation, d)
	}
	out := e.a.runAction(e.root, "core.user.forget-tailnet-device", `{"username":"mia","stableId":"nL"}`)
	if out["stableId"] != "nL" || e.devices(e.miaID) != "nP" {
		t.Errorf("admin forget output %v, mia's devices %q; want nL forgotten and nP kept", out, e.devices(e.miaID))
	}

	e.a.refused(e.root, "/api/v1/actions/core.user.list-tailnet-devices/v1/read", `{"input":{"username":"nobody"}}`,
		http.StatusNotFound, errcode.NotFound)
	e.a.refused(e.root, "/api/v1/actions/core.user.forget-tailnet-device/v1/plan", `{"input":{"username":"nobody","stableId":"nP"}}`,
		http.StatusNotFound, errcode.NotFound)

	for _, c := range []caller{e.session, e.read, e.op} {
		e.a.refused(c, "/api/v1/actions/core.user.list-tailnet-devices/v1/read", `{"input":{"username":"mia"}}`,
			http.StatusForbidden, errcode.Forbidden)
		e.a.refused(c, "/api/v1/actions/core.user.forget-tailnet-device/v1/plan", `{"input":{"username":"mia","stableId":"nP"}}`,
			http.StatusForbidden, errcode.Forbidden)
	}
	if got := e.devices(e.miaID); got != "nP" {
		t.Errorf("mia's devices after the member's calls: %q, want nP kept", got)
	}
}

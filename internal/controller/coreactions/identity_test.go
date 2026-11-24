package coreactions

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// pins returns username's stored email and sign-in pins, "" for NULL.
func (e *testEnv) pins(username string) (email, issuer, subject, login, tailnetID string) {
	e.t.Helper()
	var v [5]sql.NullString
	if err := e.db.Read().QueryRow(`SELECT email, oidc_issuer, oidc_subject, tailnet_login, tailnet_user_id
		FROM core_users WHERE username = ?`, username).Scan(&v[0], &v[1], &v[2], &v[3], &v[4]); err != nil {
		e.t.Fatal(err)
	}
	return v[0].String, v[1].String, v[2].String, v[3].String, v[4].String
}

func TestIdentitySetClearsPins(t *testing.T) {
	e := newTestEnv(t)
	_, admin := e.activeUser("root")
	e.activeUser("mia")
	e.exec(`UPDATE core_users SET email = 'mia@example.com', oidc_issuer = 'https://id.example', oidc_subject = 's-1',
		tailnet_login = 'mia@github', tailnet_user_id = 'u-123' WHERE username = 'mia'`)

	var out auth.DirectoryUser
	e.run(admin, "core.user.set-identity", IdentityInput{Username: "mia", Email: "Mia@Example.com", TailnetLogin: "mia@github"}, &out)
	if email, issuer, subject, _, tailnetID := e.pins("mia"); email != "Mia@Example.com" || issuer == "" || subject != "s-1" || tailnetID != "u-123" {
		t.Errorf("after a case-only email change: email %q, pins %q %q %q; want the new spelling and every pin kept", email, issuer, subject, tailnetID)
	}

	pv, err := e.plan(admin, "core.user.set-identity", IdentityInput{Username: "mia", Email: "mia@corp.example", TailnetLogin: "mia@github"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diffText(pv.Diff), "its OIDC link ends") {
		t.Errorf("plan diff %+v; want it to say the OIDC link ends", pv.Diff)
	}
	e.run(admin, "core.user.set-identity", IdentityInput{Username: "mia", Email: "mia@corp.example", TailnetLogin: "mia@github"}, &out)
	if email, issuer, subject, login, tailnetID := e.pins("mia"); email != "mia@corp.example" || issuer != "" || subject != "" ||
		login != "mia@github" || tailnetID != "u-123" {
		t.Errorf("after an email change: %q %q %q %q %q; want the OIDC pin cleared and the tailnet pin kept", email, issuer, subject, login, tailnetID)
	}
	if out.Email != "mia@corp.example" || out.TailnetLogin != "mia@github" {
		t.Errorf("set-identity output %+v; want the new identities", out)
	}

	e.run(admin, "core.user.set-identity", IdentityInput{Username: "mia", Email: "mia@corp.example", TailnetLogin: "mia@corp"}, nil)
	if _, _, _, login, tailnetID := e.pins("mia"); login != "mia@corp" || tailnetID != "" {
		t.Errorf("after a tailnet login change: %q %q; want the tailnet pin cleared", login, tailnetID)
	}
	e.exec(`UPDATE core_users SET oidc_issuer = 'https://id.example', oidc_subject = 's-1' WHERE username = 'mia'`)
	e.run(admin, "core.user.set-identity", IdentityInput{Username: "mia", Email: "", TailnetLogin: ""}, nil)
	if email, issuer, subject, login, _ := e.pins("mia"); email != "" || issuer != "" || subject != "" || login != "" {
		t.Errorf("after clearing: %q %q %q %q; want no email, no login and no pin", email, issuer, subject, login)
	}

	e.activeUser("ned")
	e.run(admin, "core.user.set-identity", IdentityInput{Username: "ned", Email: "Ned@Example.com", TailnetLogin: "Ned@GitHub"}, nil)
	for name, in := range map[string]IdentityInput{
		"email":         {Username: "mia", Email: "ned@example.COM"},
		"tailnet login": {Username: "mia", TailnetLogin: "ned@github"},
	} {
		_, err := e.plan(admin, "core.user.set-identity", in)
		wantCode(t, err, errcode.InvalidInput)
		if f := errcode.From(err).Fields; len(f) != 1 || f[0].Message != "another user already has it" {
			t.Errorf("%s taken in another case: %+v; want it refused as taken", name, f)
		}
	}
	_, err = e.plan(admin, "core.user.set-identity", IdentityInput{Username: "mia", Email: "not an email"})
	wantCode(t, err, errcode.InvalidInput)
	_, err = e.auth.SetIdentity(context.Background(), "mia", "NED@example.com", "")
	wantCode(t, err, errcode.InvalidInput)
}

func diffText(diff []core.DiffLine) string {
	var b strings.Builder
	for _, l := range diff {
		b.WriteString(l.Op + " " + l.Text + "\n")
	}
	return b.String()
}

func TestDirectoryIsFleetOnly(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	rootUser, admin := e.activeUser("root")
	e.exec(`INSERT INTO core_host_groups (id, name, created_at) VALUES ('g1', 'web', '2026-11-24T09:00:00.000000000Z')`)
	inv, err := e.auth.Invite(ctx, auth.InviteRequest{Username: "mia", Role: auth.RoleMember,
		Grants: []auth.Grant{{Target: "g1", Role: auth.GrantViewer}, {Target: "*", Role: auth.GrantViewer}}, Email: "mia@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	bob, _ := e.activeUser("bob")
	e.run(admin, "core.user.set-role", SetRoleInput{Username: "bob", Role: auth.RoleMember}, nil)
	e.run(admin, "core.user.set-grants", SetGrantsInput{Username: "bob", Grants: []auth.Grant{{Target: "g1", Role: auth.GrantOperator}}}, nil)
	e.exec(`UPDATE core_users SET email = 'bob@example.com', oidc_issuer = 'https://id.example', oidc_subject = 's-2',
		tailnet_login = 'bob@github', tailnet_user_id = 'u-2' WHERE username = 'bob'`)
	e.run(admin, "core.user.disable", UsernameInput{Username: "bob"}, nil)

	raw, err := e.engine.Read(ctx, admin, ref("core.user.directory"), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var got Directory
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Users) != 3 {
		t.Fatalf("directory %s; want three users", raw)
	}
	byName := map[string]auth.DirectoryUser{}
	for _, u := range got.Users {
		byName[u.Username] = u
	}
	want := map[string]auth.DirectoryUser{
		"bob": {User: auth.User{ID: bob.ID, Username: "bob", Role: auth.RoleMember, State: auth.StateActive, Disabled: true},
			Grants: []auth.Grant{{Target: "g1", Role: auth.GrantOperator}}, Email: "bob@example.com", TailnetLogin: "bob@github",
			SignIn: []string{auth.SignInPassword, auth.SignInOIDC, auth.SignInTailscale}},
		"mia": {User: auth.User{Username: "mia", Role: auth.RoleMember, State: auth.StatePending},
			Grants: []auth.Grant{{Target: "*", Role: auth.GrantViewer}, {Target: "g1", Role: auth.GrantViewer}},
			Email:  "mia@example.com", SignIn: []string{}, InviteExpiresAt: &inv.ExpiresAt},
		"root": {User: auth.User{ID: rootUser.ID, Username: "root", Role: auth.RoleAdmin, State: auth.StateActive},
			Grants: []auth.Grant{}, SignIn: []string{auth.SignInPassword}},
	}
	for name, w := range want {
		g := byName[name]
		if w.ID == "" {
			w.ID = g.ID
		}
		w.CreatedAt = g.CreatedAt
		if w.InviteExpiresAt != nil && g.InviteExpiresAt != nil && w.InviteExpiresAt.Equal(*g.InviteExpiresAt) {
			w.InviteExpiresAt = g.InviteExpiresAt
		}
		if !reflect.DeepEqual(g, w) {
			t.Errorf("directory entry %s = %+v, want %+v", name, g, w)
		}
	}
	if !strings.Contains(string(raw), `"inviteExpiresAt":null`) {
		t.Errorf("directory %s; want an active user's invite expiry as null", raw)
	}
	if !strings.Contains(string(raw), `"grants":[]`) || !strings.Contains(string(raw), `"signIn":[]`) {
		t.Errorf("directory %s; want empty grants and sign-in lists as []", raw)
	}

	if _, err := e.auth.ResetPassword(ctx, "mia", testPassword); err != nil {
		t.Fatal(err)
	}
	if err := e.read(admin, "core.user.directory", NoInput{}, &got); err != nil {
		t.Fatal(err)
	}
	for _, u := range got.Users {
		if u.Username == "mia" && !reflect.DeepEqual(u.SignIn, []string{auth.SignInPassword}) {
			t.Errorf("mia's sign-in after a password reset = %v, want password", u.SignIn)
		}
		if u.Username == "mia" && u.InviteExpiresAt != nil {
			t.Errorf("mia's invite expiry after a password reset ended the link = %v, want null", u.InviteExpiresAt)
		}
	}

	_, err = e.plan(admin, "core.user.set-grants", SetGrantsInput{Username: "root", Grants: []auth.Grant{{Target: "*", Role: auth.GrantViewer}}})
	wantCode(t, err, errcode.InvalidInput)
	_, err = e.auth.SetGrants(ctx, "root", []auth.Grant{{Target: "*", Role: auth.GrantViewer}})
	wantCode(t, err, errcode.InvalidInput)
	_, err = e.auth.SetGrants(ctx, "bob", []auth.Grant{{Target: "gone", Role: auth.GrantViewer}})
	wantCode(t, err, errcode.InvalidInput)
	_, err = e.auth.Invite(ctx, auth.InviteRequest{Username: "zed", Role: auth.RoleMember,
		Grants: []auth.Grant{{Target: "gone", Role: auth.GrantViewer}}})
	wantCode(t, err, errcode.InvalidInput)
	if _, err := e.auth.GetUser(ctx, "zed"); errcode.From(err).Code != errcode.NotFound {
		t.Errorf("an invite with an unknown group left user zed: %v", err)
	}

	adminRead := core.Principal{Actor: "user:" + rootUser.ID, UserID: rootUser.ID, Source: "127.0.0.1",
		Access: core.Access{Fleet: true, All: core.ScopeRead}}
	if err := e.read(adminRead, "core.user.directory", NoInput{}, &got); err != nil {
		t.Errorf("an admin's read token: %v", err)
	}
	member := core.Principal{Actor: "user:" + bob.ID, UserID: bob.ID, Source: "127.0.0.1",
		Access: core.Access{All: core.ScopeOperate}}
	wantCode(t, e.read(member, "core.user.directory", NoInput{}, &got), errcode.Forbidden)

	def, ok := e.reg.Action(ref("core.user.directory"))
	if !ok || def.Spec.HostData != core.HostDataFleet || def.Spec.Class != core.ClassRead {
		t.Errorf("core.user.directory declared %+v; want a fleet read", def.Spec)
	}
}

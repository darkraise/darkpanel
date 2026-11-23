package auth

import (
	"context"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

const localHost = "local"

func exec(t *testing.T, s *Service, query string, args ...any) {
	t.Helper()
	if _, err := s.db.Writer().Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// addHosts adds hosts; the first call with localHost also records it as the
// controller's own host, as the server does on its first start.
func addHosts(t *testing.T, s *Service, ids ...string) {
	t.Helper()
	for _, id := range ids {
		local := 0
		if id == localHost {
			local = 1
			exec(t, s, `INSERT INTO core_meta (key, value) VALUES ('local_host_id', ?)`, id)
		}
		exec(t, s, `INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, ?, ?)`,
			id, id, local, formatTime(s.now()))
	}
}

func addGroup(t *testing.T, s *Service, id string, hosts ...string) {
	t.Helper()
	exec(t, s, `INSERT INTO core_host_groups (id, name, created_at) VALUES (?, ?, ?)`, id, "group "+id, formatTime(s.now()))
	for _, h := range hosts {
		exec(t, s, `INSERT INTO core_host_group_members (group_id, host_id) VALUES (?, ?)`, id, h)
	}
}

func addGrant(t *testing.T, s *Service, userID, target, role string) {
	t.Helper()
	exec(t, s, `INSERT INTO core_user_grants (user_id, target, role) VALUES (?, ?, ?)`, userID, target, role)
}

func setRole(t *testing.T, s *Service, u *User, role string) {
	t.Helper()
	exec(t, s, `UPDATE core_users SET role = ? WHERE id = ?`, role, u.ID)
	u.Role = role
}

func TestResolveAccess(t *testing.T) {
	s, clock, _ := newTestService(t)
	addHosts(t, s, localHost, "h1", "h2", "h3")
	addGroup(t, s, "G", "h1", "h2")
	addGroup(t, s, "L", localHost, "h3")
	admin, _ := createActive(t, s, clock, "admin")
	mixed, _ := createActive(t, s, clock, "mixed")
	setRole(t, s, &mixed, RoleMember)
	addGrant(t, s, mixed.ID, "G", "operator")
	addGrant(t, s, mixed.ID, "*", "viewer")
	none, _ := createActive(t, s, clock, "none")
	setRole(t, s, &none, RoleMember)
	onLocal, _ := createActive(t, s, clock, "onlocal")
	setRole(t, s, &onLocal, RoleMember)
	addGrant(t, s, onLocal.ID, "L", "operator")
	star, _ := createActive(t, s, clock, "star")
	setRole(t, s, &star, RoleMember)
	addGrant(t, s, star.ID, "*", "operator")
	viewerG, _ := createActive(t, s, clock, "viewerg")
	setRole(t, s, &viewerG, RoleMember)
	addGrant(t, s, viewerG.ID, "G", "viewer")

	capped := map[string]core.Scope{localHost: core.ScopeRead}
	cases := []struct {
		name  string
		user  User
		token core.Scope
		want  core.Access
	}{
		{"admin", admin, "", core.Access{Admin: true, Fleet: true}},
		{"admin, admin token", admin, core.ScopeAdmin, core.Access{Admin: true, Fleet: true}},
		{"admin, read token", admin, core.ScopeRead, core.Access{Fleet: true, All: core.ScopeRead}},
		{"admin, operate token", admin, core.ScopeOperate, core.Access{Fleet: true, All: core.ScopeOperate}},
		{"operator on G, viewer on *", mixed, "", core.Access{All: core.ScopeRead,
			Hosts: map[string]core.Scope{"h1": core.ScopeOperate, "h2": core.ScopeOperate}, Ceil: capped}},
		{"no grants", none, "", core.Access{}},
		{"operator on a group holding the controller", onLocal, "", core.Access{
			Hosts: map[string]core.Scope{localHost: core.ScopeRead, "h3": core.ScopeOperate}, Ceil: capped}},
		{"operator on *", star, "", core.Access{All: core.ScopeOperate, Ceil: capped}},
		{"operator on *, read token", star, core.ScopeRead, core.Access{All: core.ScopeRead, Ceil: capped}},
		{"viewer on G, operate token", viewerG, core.ScopeOperate, core.Access{
			Hosts: map[string]core.Scope{"h1": core.ScopeRead, "h2": core.ScopeRead}}},
		{"operator on G, viewer on *, read token", mixed, core.ScopeRead, core.Access{All: core.ScopeRead,
			Hosts: map[string]core.Scope{"h1": core.ScopeRead, "h2": core.ScopeRead}, Ceil: capped}},
	}
	for _, c := range cases {
		got, err := resolveAccess(context.Background(), s.db.Read(), c.user, c.token)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: access = %+v, want %+v", c.name, got, c.want)
		}
	}
	if got := mustAccess(t, s, star, "").On(localHost); got != core.ScopeRead {
		t.Errorf("operator on *: On(controller) = %q, want read", got)
	}
}

func mustAccess(t *testing.T, s *Service, u User, token core.Scope) core.Access {
	t.Helper()
	a, err := resolveAccess(context.Background(), s.db.Read(), u, token)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestSessionPrincipalCarriesAccess(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	addHosts(t, s, localHost, "h1")
	addGroup(t, s, "G", "h1")
	member, _ := createActive(t, s, clock, "member")
	setRole(t, s, &member, RoleMember)
	addGrant(t, s, member.ID, "G", "operator")
	admin, _ := createActive(t, s, clock, "admin")

	id, _ := newSession(t, s, member.ID)
	got, err := s.Authenticate(ctx, Credentials{Session: id, Source: "10.0.0.2"})
	if err != nil {
		t.Fatalf("member session: %v", err)
	}
	want := core.Access{Hosts: map[string]core.Scope{"h1": core.ScopeOperate}}
	if got.Principal.Scope != "" || !reflect.DeepEqual(got.Principal.Access, want) {
		t.Errorf("member principal = %+v, want scope \"\" and access %+v", got.Principal, want)
	}

	id, _ = newSession(t, s, admin.ID)
	got, err = s.Authenticate(ctx, Credentials{Session: id, Source: "10.0.0.2"})
	if err != nil {
		t.Fatalf("admin session: %v", err)
	}
	if got.Principal.Scope != core.ScopeAdmin || !got.Principal.Access.Fleet || !got.Principal.Access.Admin {
		t.Errorf("admin principal = %+v, want scope admin with admin and fleet access", got.Principal)
	}

	exec(t, s, `UPDATE core_users SET role = 'auditor' WHERE id = ?`, member.ID)
	id, _ = newSession(t, s, member.ID)
	if _, err := s.Authenticate(ctx, Credentials{Session: id}); err == nil {
		t.Error("a session of a user with an unknown role authenticated")
	}
}

func bearer(token string) Credentials {
	return Credentials{Authorization: []string{"Bearer " + token}, Source: "10.0.0.3"}
}

func TestTokenNeverExceedsItsUser(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	addHosts(t, s, localHost, "h1", "h2")
	addGroup(t, s, "G", "h1")
	admin, _ := createActive(t, s, clock, "admin")
	member, _ := createActive(t, s, clock, "member")
	setRole(t, s, &member, RoleMember)
	addGrant(t, s, member.ID, "G", "viewer")

	_, readToken, err := s.CreateToken(ctx, admin.ID, "read", core.ScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Authenticate(ctx, bearer(readToken))
	if err != nil {
		t.Fatal(err)
	}
	if want := (core.Access{Fleet: true, All: core.ScopeRead}); !reflect.DeepEqual(got.Principal.Access, want) ||
		got.Principal.Scope != "" {
		t.Errorf("admin read token principal = %+v, want scope \"\" and access %+v", got.Principal, want)
	}

	_, operateToken, err := s.CreateToken(ctx, member.ID, "operate", core.ScopeOperate, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.Authenticate(ctx, bearer(operateToken))
	if err != nil {
		t.Fatal(err)
	}
	if want := (core.Access{Hosts: map[string]core.Scope{"h1": core.ScopeRead}}); !reflect.DeepEqual(got.Principal.Access, want) ||
		got.Principal.Scope != "" {
		t.Errorf("member operate token principal = %+v, want scope \"\" and access %+v", got.Principal, want)
	}

	demoted, _ := createActive(t, s, clock, "demoted")
	_, adminToken, err := s.CreateToken(ctx, demoted.ID, "admin", core.ScopeAdmin, 0)
	if err != nil {
		t.Fatal(err)
	}
	setRole(t, s, &demoted, RoleMember)
	got, err = s.Authenticate(ctx, bearer(adminToken))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Principal.Access, core.Access{}) || got.Principal.Scope != "" {
		t.Errorf("demoted admin token principal = %+v, want scope \"\" and no access", got.Principal)
	}

	setRole(t, s, &demoted, "auditor")
	if _, err := s.Authenticate(ctx, bearer(adminToken)); err == nil {
		t.Error("a token of a user with an unknown role authenticated")
	}
}

func TestGrantChangeAppliesNextRequest(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	addHosts(t, s, localHost, "h1", "h2")
	addGroup(t, s, "G", "h1", "h2")
	member, _ := createActive(t, s, clock, "member")
	setRole(t, s, &member, RoleMember)
	addGrant(t, s, member.ID, "G", "viewer")
	id, _ := newSession(t, s, member.ID)
	access := func() core.Access {
		t.Helper()
		got, err := s.Authenticate(ctx, Credentials{Session: id})
		if err != nil {
			t.Fatal(err)
		}
		return got.Principal.Access
	}

	if got := access().On("h2"); got != core.ScopeRead {
		t.Fatalf("On(h2) = %q before the change, want read", got)
	}
	exec(t, s, `DELETE FROM core_host_group_members WHERE group_id = 'G' AND host_id = 'h2'`)
	if got := access(); got.On("h2") != "" || got.On("h1") != core.ScopeRead {
		t.Errorf("after removing h2 from G: On(h2) = %q, On(h1) = %q; want \"\" and read", got.On("h2"), got.On("h1"))
	}

	addGrant(t, s, member.ID, "*", "viewer")
	addHosts(t, s, "h9")
	if got := access().On("h9"); got != core.ScopeRead {
		t.Errorf("On(h9), enrolled after the * grant = %q, want read", got)
	}
}

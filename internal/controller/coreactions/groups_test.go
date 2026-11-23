package coreactions

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// members returns the stored host ids of group id, sorted.
func (e *testEnv) members(id string) []string {
	e.t.Helper()
	rows, err := e.db.Read().Query(`SELECT host_id FROM core_host_group_members WHERE group_id = ? ORDER BY host_id`, id)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, h)
	}
	return out
}

func TestGroupLifecycle(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	_, admin := e.activeUser("root")
	e.addHost("h1", "web-1", false)
	e.addHost("h2", "db-1", false)

	var web Group
	e.run(admin, "core.group.create", GroupCreateInput{Name: "web"}, &web)
	if web.ID == "" || web.Name != "web" || len(web.HostIDs) != 0 {
		t.Fatalf("create output %+v; want group web with no hosts", web)
	}
	long := strings.Repeat("a", 48)
	e.run(admin, "core.group.create", GroupCreateInput{Name: long}, nil)
	for _, name := range []string{"WEB", "", " web", "-web", strings.Repeat("a", 49), "web/1"} {
		_, err := e.plan(admin, "core.group.create", GroupCreateInput{Name: name})
		wantCode(t, err, errcode.InvalidInput)
	}

	e.run(admin, "core.group.rename", GroupRenameInput{ID: web.ID, Name: "Web servers"}, nil)
	var name string
	if err := e.db.Read().QueryRow(`SELECT name FROM core_host_groups WHERE id = ?`, web.ID).Scan(&name); err != nil || name != "Web servers" {
		t.Errorf("after rename the name is %q, %v; want Web servers", name, err)
	}
	_, err := e.plan(admin, "core.group.rename", GroupRenameInput{ID: web.ID, Name: long})
	wantCode(t, err, errcode.InvalidInput)
	_, err = e.plan(admin, "core.group.rename", GroupRenameInput{ID: "nope", Name: "x"})
	wantCode(t, err, errcode.NotFound)

	e.run(admin, "core.group.set-members", GroupMembersInput{ID: web.ID, HostIDs: []string{"h2", "h1"}}, nil)
	if got := e.members(web.ID); !reflect.DeepEqual(got, []string{"h1", "h2"}) {
		t.Errorf("members %v; want h1 h2", got)
	}
	e.run(admin, "core.group.set-members", GroupMembersInput{ID: web.ID, HostIDs: []string{"h1"}}, nil)
	if got := e.members(web.ID); !reflect.DeepEqual(got, []string{"h1"}) {
		t.Errorf("members %v; want h1", got)
	}
	for _, hosts := range [][]string{{"nope"}, {"h1", "h1"}} {
		_, err := e.plan(admin, "core.group.set-members", GroupMembersInput{ID: web.ID, HostIDs: hosts})
		wantCode(t, err, errcode.InvalidInput)
	}

	if _, err := e.auth.Invite(ctx, auth.InviteRequest{Username: "mia", Role: auth.RoleMember,
		Grants: []auth.Grant{{Target: web.ID, Role: auth.GrantViewer}}}); err != nil {
		t.Fatal(err)
	}
	var list GroupList
	if err := e.read(admin, "core.group.list", NoInput{}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Groups) != 2 {
		t.Fatalf("list %+v; want two groups", list)
	}
	got := list.Groups[1]
	if got.Name != "Web servers" || got.HostCount != 1 || !reflect.DeepEqual(got.HostIDs, []string{"h1"}) || got.GrantCount != 1 {
		t.Errorf("list entry %+v; want Web servers with one host and one grant", got)
	}
	def, _ := e.reg.Action(ref("core.group.list"))
	if def.Spec.HostData != "fleet" {
		t.Errorf("core.group.list HostData %q, want fleet", def.Spec.HostData)
	}

	remove := RegisterGroupReferrer("test.rules", func(_ context.Context, q GroupQuerier, groupID string) ([]string, error) {
		var n int
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM core_host_groups WHERE id = ?`, groupID).Scan(&n); err != nil {
			return nil, err
		}
		if groupID == web.ID {
			return []string{"alert rule cpu high"}, nil
		}
		return nil, nil
	})
	_, err = e.plan(admin, "core.group.delete", GroupIDInput{ID: web.ID})
	wantCode(t, err, errcode.GroupInUse)
	if msg := errcode.From(err).Detail; !strings.Contains(msg, "grant of mia (viewer)") || !strings.Contains(msg, "alert rule cpu high") {
		t.Errorf("in-use refusal %q; want it to name mia's grant and the alert rule", msg)
	}
	e.run(admin, "core.user.cancel-invite", UsernameInput{Username: "mia"}, nil)
	_, err = e.plan(admin, "core.group.delete", GroupIDInput{ID: web.ID})
	wantCode(t, err, errcode.GroupInUse)
	if msg := errcode.From(err).Detail; strings.Contains(msg, "mia") {
		t.Errorf("in-use refusal %q still names mia's removed grant", msg)
	}
	remove()

	pv, err := e.plan(admin, "core.group.delete", GroupIDInput{ID: web.ID})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Confirmation != "Web servers" {
		t.Errorf("delete confirmation %q, want the group's name", pv.Confirmation)
	}
	e.run(admin, "core.group.delete", GroupIDInput{ID: web.ID}, nil)
	var n int
	if err := e.db.Read().QueryRow(`SELECT count(*) FROM core_host_group_members WHERE group_id = ?`, web.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("members left after delete: %d, %v", n, err)
	}
	if err := e.db.Read().QueryRow(`SELECT name FROM core_host_groups WHERE id = ?`, web.ID).Scan(&name); err != sql.ErrNoRows {
		t.Errorf("group still stored after delete: %v", err)
	}
	_, err = e.plan(admin, "core.group.delete", GroupIDInput{ID: web.ID})
	wantCode(t, err, errcode.NotFound)
}

// TestGroupDeleteRechecksInTheTransaction: a grant added between the plan
// and the apply still refuses the delete.
func TestGroupDeleteRechecksInTheTransaction(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	_, admin := e.activeUser("root")
	var web Group
	e.run(admin, "core.group.create", GroupCreateInput{Name: "web"}, &web)
	pv, err := e.plan(admin, "core.group.delete", GroupIDInput{ID: web.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.Invite(ctx, auth.InviteRequest{Username: "mia", Role: auth.RoleMember,
		Grants: []auth.Grant{{Target: web.ID, Role: auth.GrantOperator}}}); err != nil {
		t.Fatal(err)
	}
	v, err := e.apply(admin, pv.PlanID, pv.Confirmation)
	if err == nil && (v.Error == nil || v.Error.Code != string(errcode.GroupInUse)) {
		t.Errorf("apply after a grant appeared: %+v; want group.in_use", v)
	}
	if err != nil && errcode.From(err).Code != errcode.GroupInUse {
		t.Errorf("apply after a grant appeared: %v; want group.in_use", err)
	}
	var n int
	if err := e.db.Read().QueryRow(`SELECT count(*) FROM core_host_groups WHERE id = ?`, web.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("group rows %d, %v; want the group kept", n, err)
	}
}

// TestGroupDeleteApplyRefusesAGrant: the apply itself refuses a group a
// grant targets, closing the window between the engine's last plan and the
// write.
func TestGroupDeleteApplyRefusesAGrant(t *testing.T) {
	e := newTestEnv(t)
	_, admin := e.activeUser("root")
	var web Group
	e.run(admin, "core.group.create", GroupCreateInput{Name: "web"}, &web)
	if _, err := e.auth.Invite(context.Background(), auth.InviteRequest{Username: "mia", Role: auth.RoleMember,
		Grants: []auth.Grant{{Target: web.ID, Role: auth.GrantViewer}}}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.applyDirect("core.group.delete", GroupIDInput{ID: web.ID}), errcode.GroupInUse)
	if _, err := loadGroup(context.Background(), e.db.Read(), web.ID); err != nil {
		t.Errorf("group after a refused delete: %v; want it kept", err)
	}
}

package auth_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestLastAdminIsProtected(t *testing.T) {
	e := newInviteEnv(t)
	ctx := context.Background()
	root, _ := e.active("root")

	for name, input := range map[string]string{
		"core.user.set-role": `{"username":"root","role":"member"}`,
		"core.user.disable":  `{"username":"root"}`,
	} {
		_, err := e.plan(root, name, input)
		wantErrCode(t, name+" of the last active admin", err, errcode.UserLastAdmin)
	}
	_, err := e.auth.SetRole(ctx, "root", auth.RoleMember)
	wantErrCode(t, "SetRole of the last active admin", err, errcode.UserLastAdmin)
	_, err = e.auth.SetDisabled(ctx, "root", true)
	wantErrCode(t, "SetDisabled of the last active admin", err, errcode.UserLastAdmin)
	wantErrCode(t, "break-glass delete of the last active admin", e.auth.DeleteUser(ctx, "root"), errcode.UserLastAdmin)
	_, err = e.plan(root, "core.user.delete", `{"username":"root"}`)
	wantErrCode(t, "break-glass plan to delete the last active admin", err, errcode.UserLastAdmin)

	e.active("ann")
	cv := e.apply(root, "core.user.set-role", `{"username":"ann","role":"member"}`)
	var out auth.DirectoryUser
	if err := json.Unmarshal(cv.Output, &out); err != nil {
		t.Fatal(err)
	}
	if out.Role != auth.RoleMember || out.Username != "ann" {
		t.Errorf("set-role output %s; want ann as a member", cv.Output)
	}
	if u, _ := e.auth.GetUser(ctx, "ann"); u.Role != auth.RoleMember {
		t.Errorf("ann is %s after the demotion, want member", u.Role)
	}
	_, err = e.plan(root, "core.user.set-role", `{"username":"root","role":"member"}`)
	wantErrCode(t, "demoting root once ann is a member", err, errcode.UserLastAdmin)
	e.apply(root, "core.user.set-role", `{"username":"ann","role":"admin"}`)
	e.apply(root, "core.user.set-role", `{"username":"root","role":"member"}`)
	if u, _ := e.auth.GetUser(ctx, "root"); u.Role != auth.RoleMember {
		t.Errorf("root is %s with ann an admin again, want member", u.Role)
	}
	_, err = e.auth.SetRole(ctx, "ann", auth.RoleMember)
	wantErrCode(t, "SetRole of ann, now the last active admin", err, errcode.UserLastAdmin)

	ann, _ := e.auth.GetUser(ctx, "ann")
	admin := core.Principal{Actor: "user:" + ann.ID, UserID: ann.ID, Scope: core.ScopeAdmin, Source: "127.0.0.1",
		Access: core.Access{Admin: true, Fleet: true}}
	inv, err := e.auth.Invite(ctx, auth.InviteRequest{Username: "carl", Role: auth.RoleMember})
	if err != nil {
		t.Fatal(err)
	}
	start, err := e.auth.BeginEnrollment(ctx, inv.Token, "192.0.2.1", false)
	if err != nil {
		t.Fatal(err)
	}
	e.apply(admin, "core.user.set-role", `{"username":"carl","role":"admin"}`)
	_, err = e.auth.FinishEnrollment(ctx, auth.EnrollRequest{Token: inv.Token, Password: invitePassword,
		Code: e.code(start.Secret), Source: "192.0.2.1"})
	wantErrCode(t, "enrollment after a role change", err, errcode.EnrollmentExpired)
	_, err = e.auth.BeginEnrollment(ctx, inv.Token, "192.0.2.1", false)
	wantErrCode(t, "begin after a role change", err, errcode.EnrollmentExpired)
	renewed := e.apply(admin, "core.user.renew-invite", `{"username":"carl"}`)
	var again coreactions.Invitation
	if err := json.Unmarshal(renewed.Output, &again); err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.BeginEnrollment(ctx, again.Token, "192.0.2.1", false); err != nil {
		t.Errorf("begin with the link renewed after the role change: %v", err)
	}

	for name, input := range map[string]string{
		"unknown role": `{"username":"carl","role":"owner"}`,
		"unknown user": `{"username":"nobody","role":"member"}`,
	} {
		if _, err := e.plan(admin, "core.user.set-role", input); err == nil {
			t.Errorf("set-role, %s: planned", name)
		}
	}
}

func TestCreateKeepsAdminDefault(t *testing.T) {
	e := newInviteEnv(t)
	ctx := context.Background()
	socket := core.Principal{Actor: "local:root@h1", Scope: core.ScopeAdmin, Source: "socket"}

	cv := e.apply(socket, "core.user.create", `{"username":"ada","password":"`+invitePassword+`"}`)
	var ada coreactions.Enrollment
	if err := json.Unmarshal(cv.Output, &ada); err != nil {
		t.Fatal(err)
	}
	if ada.User.Role != auth.RoleAdmin {
		t.Errorf("create without a role made a %s, want admin", ada.User.Role)
	}

	cv = e.apply(socket, "core.user.create", `{"username":"mel","password":"`+invitePassword+`","role":"member"}`)
	var mel coreactions.Enrollment
	if err := json.Unmarshal(cv.Output, &mel); err != nil {
		t.Fatal(err)
	}
	if mel.User.Role != auth.RoleMember || mel.User.State != auth.StatePending {
		t.Fatalf("create with role member made %+v, want a pending member", mel.User)
	}
	if _, err := e.auth.ActivateUser(ctx, "mel", e.code(mel.TOTPSecret)); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(auth.TOTPPeriod)
	login, err := e.auth.Login(ctx, auth.LoginRequest{Username: "mel", Password: invitePassword,
		Code: e.code(mel.TOTPSecret), Source: "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	if a := login.Access; a.Admin || a.Fleet || a.All != "" || len(a.Hosts) != 0 || a.Everywhere() != "" {
		t.Errorf("a new member's access = %+v, want none", a)
	}

	if _, err := e.plan(socket, "core.user.create", `{"username":"oz","password":"`+invitePassword+`","role":"owner"}`); err == nil {
		t.Error("create with role owner: planned")
	}
}

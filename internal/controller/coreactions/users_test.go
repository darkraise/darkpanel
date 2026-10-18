package coreactions

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// newPassword is the password reset tests set; tests scan for it.
const newPassword = "an entirely new passphrase"

// login logs username in through the identity service with a code from
// totpSecret.
func (e *testEnv) login(username, password, totpSecret string) error {
	e.t.Helper()
	code, err := auth.TOTPCode(totpSecret, e.clock.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	_, err = e.auth.Login(context.Background(), auth.LoginRequest{Username: username, Password: password, Code: code, Source: "127.0.0.1"})
	return err
}

// change returns a change as stored, with its output redacted.
func (e *testEnv) change(id string) core.ChangeView {
	e.t.Helper()
	v, err := e.engine.Change(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

// auditEntries returns every audit entry, newest first.
func (e *testEnv) auditEntries() []core.AuditEntry {
	e.t.Helper()
	entries, _, err := e.audit.List(context.Background(), core.AuditQuery{Limit: 500})
	if err != nil {
		e.t.Fatal(err)
	}
	return entries
}

func TestUserList(t *testing.T) {
	e := newTestEnv(t)
	e.activeUser("bob")
	if _, _, err := e.auth.CreateUser(context.Background(), "alice", testPassword); err != nil {
		t.Fatal(err)
	}
	var got UserList
	if err := e.read(root, "core.user.list", NoInput{}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Users) != 2 || got.Users[0].Username != "alice" || got.Users[0].State != auth.StatePending ||
		got.Users[1].Username != "bob" || got.Users[1].State != auth.StateActive {
		t.Errorf("users = %+v, want pending alice then active bob", got.Users)
	}
}

func TestUserResetTOTP(t *testing.T) {
	e := newTestEnv(t)
	u, _ := e.activeUser("alice")
	pv, err := e.plan(root, "core.user.reset-totp", UsernameInput{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{
		{Op: "-", Text: "user alice: active, with its enrolled authenticator"},
		{Op: "+", Text: "user alice: pending, with a new TOTP secret shown once; its sessions end and its API tokens stay"},
		{Op: " ", Text: "core.user.activate with a code from the new secret makes the user active again"},
		{Op: " ", Text: "alice is the last active admin: until it is activated again, no one can log in"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || len(pv.Guards) != 0 || pv.Confirmation != "alice" {
		t.Errorf("plan = %+v, want diff %+v and phrase alice", pv, wantDiff)
	}
	_, err = e.apply(root, pv.PlanID, "")
	wantCode(t, err, errcode.PlanConfirmationNeeded)
	v, err := e.apply(root, pv.PlanID, "alice")
	if err != nil || v.State != core.StateCompleted {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	var out Enrollment
	if err := json.Unmarshal(v.Output, &out); err != nil {
		t.Fatal(err)
	}
	if out.User.ID != u.ID || out.User.State != auth.StatePending || len(out.TOTPSecret) != 32 {
		t.Errorf("output = %+v, want alice pending with a 32-character secret", out)
	}
	stored := e.change(v.ID)
	if !strings.Contains(string(stored.Output), `"totpSecret":"[redacted]"`) {
		t.Errorf("stored output = %s, want the secret redacted", stored.Output)
	}
	noSecret(t, []string{out.TOTPSecret}, pv, stored, e.auditEntries())
	if err := e.verify("core.user.reset-totp", UsernameInput{Username: "alice"}); err != nil {
		t.Errorf("verify: %v", err)
	}
	if code, err := auth.TOTPCode(out.TOTPSecret, e.clock.Now()); err != nil {
		t.Fatal(err)
	} else if _, err := e.auth.ActivateUser(context.Background(), "alice", code); err != nil {
		t.Errorf("activating with the new secret: %v", err)
	}
}

func TestUserResetPassword(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	_, totp, err := e.auth.CreateUser(ctx, "alice", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TOTPCode(totp, e.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.ActivateUser(ctx, "alice", code); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(auth.TOTPPeriod)

	in := UserPasswordInput{Username: "alice", Password: newPassword}
	pv, err := e.plan(root, "core.user.reset-password", in)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{
		{Op: "-", Text: "user alice: current password"},
		{Op: "+", Text: "user alice: new password; its sessions end and its API tokens stay"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || pv.Confirmation != "alice" {
		t.Errorf("plan = %+v, want diff %+v and phrase alice", pv, wantDiff)
	}
	v, err := e.apply(root, pv.PlanID, "alice")
	if err != nil || v.State != core.StateCompleted {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	noSecret(t, []string{newPassword, testPassword}, pv, e.change(v.ID), e.auditEntries())
	if err := e.verify("core.user.reset-password", in); err != nil {
		t.Errorf("verify: %v", err)
	}
	if err := e.login("alice", newPassword, totp); err != nil {
		t.Errorf("login with the new password: %v", err)
	}
}

func TestUserPasswordRefusalsKeepThePasswordSecret(t *testing.T) {
	e := newTestEnv(t)
	e.activeUser("alice")
	for _, password := range []string{"short-pw-11", strings.Repeat("p", 129)} {
		_, err := e.plan(root, "core.user.reset-password", UserPasswordInput{Username: "alice", Password: password})
		var ce *errcode.Error
		if !errors.As(err, &ce) || ce.Code != errcode.InvalidInput || len(ce.Fields) != 1 || ce.Fields[0].Path != "/password" {
			t.Errorf("password of %d characters: err = %+v, want request.invalid_input at /password", len(password), err)
		}
		noSecret(t, []string{password}, err)
	}
	_, err := e.plan(root, "core.user.reset-password", UserPasswordInput{Username: "nobody", Password: newPassword})
	wantCode(t, err, errcode.NotFound)
	noSecret(t, []string{newPassword}, err)
}

func TestUserDelete(t *testing.T) {
	e := newTestEnv(t)
	e.activeUser("alice")
	bob, _ := e.activeUser("bob")
	if _, _, err := e.auth.CreateToken(context.Background(), bob.ID, "ci", core.ScopeRead, 0); err != nil {
		t.Fatal(err)
	}

	pv, err := e.plan(root, "core.user.delete", UsernameInput{Username: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{{Op: "-", Text: "user bob (active, role admin), with its sessions and API tokens"}}
	wantGuards := []core.Guard{{ID: "not-last-admin", OK: true, Message: "bob is not the last active admin"}}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || !reflect.DeepEqual(pv.Guards, wantGuards) || pv.Confirmation != "bob" {
		t.Errorf("plan = %+v, want diff %+v, guards %+v, phrase bob", pv, wantDiff, wantGuards)
	}
	var out Empty
	v := e.run(root, "core.user.delete", UsernameInput{Username: "bob"}, &out)
	if string(v.Output) != "{}" {
		t.Errorf("output = %s, want {}", v.Output)
	}
	if _, err := e.auth.GetUser(context.Background(), "bob"); err == nil {
		t.Error("bob still exists")
	}
	if tokens, err := e.auth.ListTokens(context.Background(), bob.ID); err != nil || len(tokens) != 0 {
		t.Errorf("bob's tokens = %+v, %v; want none", tokens, err)
	}
	if err := e.verify("core.user.delete", UsernameInput{Username: "bob"}); err != nil {
		t.Errorf("verify: %v", err)
	}
	if err := e.verify("core.user.delete", UsernameInput{Username: "alice"}); err == nil {
		t.Error("verify passed for a user that exists")
	}

	pv, err = e.plan(root, "core.user.delete", UsernameInput{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	want := []core.Guard{{ID: "not-last-admin", Message: "alice is the last active admin; create and activate another admin first"}}
	if !reflect.DeepEqual(pv.Guards, want) {
		t.Errorf("guards = %+v, want %+v", pv.Guards, want)
	}
	_, err = e.apply(root, pv.PlanID, "alice")
	wantCode(t, err, errcode.PlanGuardFailed)
}

func TestUserDeleteStateChanged(t *testing.T) {
	e := newTestEnv(t)
	e.activeUser("alice")
	e.activeUser("bob")
	pv, err := e.plan(root, "core.user.delete", UsernameInput{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.auth.DeleteUser(context.Background(), "bob"); err != nil {
		t.Fatal(err)
	}
	_, err = e.apply(root, pv.PlanID, "alice")
	wantCode(t, err, errcode.PlanStateChanged)
}

func TestUserActionsRefuseUnknownUsers(t *testing.T) {
	e := newTestEnv(t)
	for _, action := range []string{"core.user.reset-totp", "core.user.delete"} {
		_, err := e.plan(root, action, UsernameInput{Username: "nobody"})
		wantCode(t, err, errcode.NotFound)
	}
	_, err := e.plan(root, "core.user.delete", UsernameInput{Username: "Not Valid"})
	wantCode(t, err, errcode.InvalidInput)
}

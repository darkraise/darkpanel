package coreactions

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// wrongCode returns a six-digit code that matches none of the steps
// ActivateUser accepts for totpSecret now.
func (e *testEnv) wrongCode(totpSecret string) string {
	e.t.Helper()
	var valid []string
	for _, step := range []int{-1, 0, 1} {
		c, err := auth.TOTPCode(totpSecret, e.clock.Now().Add(auth.TOTPPeriod*time.Duration(step)))
		if err != nil {
			e.t.Fatal(err)
		}
		valid = append(valid, c)
	}
	for _, c := range []string{"918273", "827364", "736455", "645546"} {
		if !slices.Contains(valid, c) {
			return c
		}
	}
	e.t.Fatal("no wrong code found")
	return ""
}

func TestUserCreateAndActivate(t *testing.T) {
	e := newTestEnv(t)
	in := UserPasswordInput{Username: "alice", Password: testPassword}
	pv, err := e.plan(root, "core.user.create", in)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{
		{Op: "+", Text: "user alice: pending admin, with a new TOTP secret shown once"},
		{Op: " ", Text: "core.user.activate with a code from the secret makes the user active"},
		{Op: " ", Text: "compensated by core.user.delete"},
	}
	wantGuards := []core.Guard{{ID: "username-free", OK: true, Message: "no user is named alice"}}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || !reflect.DeepEqual(pv.Guards, wantGuards) || pv.Confirmation != "" {
		t.Errorf("plan = %+v, want diff %+v and guards %+v", pv, wantDiff, wantGuards)
	}
	v, err := e.apply(root, pv.PlanID, "")
	if err != nil || v.State != core.StateCompleted || v.CompensatedBy != "core.user.delete" {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	var out Enrollment
	if err := json.Unmarshal(v.Output, &out); err != nil {
		t.Fatal(err)
	}
	if out.User.Username != "alice" || out.User.State != auth.StatePending || out.User.Role != auth.RoleAdmin || len(out.TOTPSecret) != 32 {
		t.Errorf("output = %+v, want pending admin alice and a 32-character secret", out)
	}
	stored := e.change(v.ID)
	if !strings.Contains(string(stored.Output), `"totpSecret":"[redacted]"`) {
		t.Errorf("stored output = %s, want the secret redacted", stored.Output)
	}
	noSecret(t, []string{testPassword, out.TOTPSecret}, pv, stored, e.auditEntries())
	if err := e.verify("core.user.create", in); err != nil {
		t.Errorf("verify create: %v", err)
	}

	code, err := auth.TOTPCode(out.TOTPSecret, e.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	act := UserActivateInput{Username: "alice", Code: code}
	pv, err = e.plan(root, "core.user.activate", act)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff = []core.DiffLine{
		{Op: "-", Text: "user alice: pending"},
		{Op: "+", Text: "user alice: active, if the code matches its authenticator"},
		{Op: " ", Text: "compensated by core.user.reset-totp"},
	}
	wantGuards = []core.Guard{{ID: "user-pending", OK: true, Message: "alice is pending"}}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || !reflect.DeepEqual(pv.Guards, wantGuards) {
		t.Errorf("plan = %+v, want diff %+v and guards %+v", pv, wantDiff, wantGuards)
	}
	v, err = e.apply(root, pv.PlanID, "")
	if err != nil || v.State != core.StateCompleted || v.CompensatedBy != "core.user.reset-totp" {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	var user auth.User
	if err := json.Unmarshal(v.Output, &user); err != nil || user.State != auth.StateActive {
		t.Errorf("output = %s, want alice active", v.Output)
	}
	noSecret(t, []string{code, out.TOTPSecret, testPassword}, pv, e.change(v.ID), e.auditEntries())
	if err := e.verify("core.user.activate", act); err != nil {
		t.Errorf("verify activate: %v", err)
	}

	e.clock.Advance(auth.TOTPPeriod)
	if err := e.login("alice", testPassword, out.TOTPSecret); err != nil {
		t.Errorf("login after activation: %v", err)
	}
}

func TestUserCreateTakenUsername(t *testing.T) {
	e := newTestEnv(t)
	e.activeUser("alice")
	in := UserPasswordInput{Username: "alice", Password: newPassword}
	pv, err := e.plan(root, "core.user.create", in)
	if err != nil {
		t.Fatal(err)
	}
	want := []core.Guard{{ID: "username-free", Message: "a user named alice already exists"}}
	if !reflect.DeepEqual(pv.Guards, want) {
		t.Errorf("guards = %+v, want %+v", pv.Guards, want)
	}
	_, err = e.apply(root, pv.PlanID, "")
	wantCode(t, err, errcode.PlanGuardFailed)
	noSecret(t, []string{newPassword}, pv, err)
}

func TestUserCreateStateChanged(t *testing.T) {
	e := newTestEnv(t)
	pv, err := e.plan(root, "core.user.create", UserPasswordInput{Username: "alice", Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.auth.CreateUser(context.Background(), "alice", newPassword); err != nil {
		t.Fatal(err)
	}
	_, err = e.apply(root, pv.PlanID, "")
	wantCode(t, err, errcode.PlanStateChanged)
}

func TestUserActivateWrongCode(t *testing.T) {
	e := newTestEnv(t)
	_, totp, err := e.auth.CreateUser(context.Background(), "alice", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	wrong := e.wrongCode(totp)
	pv, err := e.plan(root, "core.user.activate", UserActivateInput{Username: "alice", Code: wrong})
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.apply(root, pv.PlanID, "")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateFailed || v.Error == nil || v.Error.Code != string(errcode.InvalidInput) {
		t.Errorf("change = %+v, want failed with request.invalid_input", v)
	}
	noSecret(t, []string{wrong, totp}, pv, v, e.auditEntries())
	if u, err := e.auth.GetUser(context.Background(), "alice"); err != nil || u.State != auth.StatePending {
		t.Errorf("alice = %+v, %v; want still pending", u, err)
	}

	for _, code := range []string{"12345", "1234567", "12a456"} {
		_, err := e.plan(root, "core.user.activate", UserActivateInput{Username: "alice", Code: code})
		var ce *errcode.Error
		if !errors.As(err, &ce) || ce.Code != errcode.InvalidInput || len(ce.Fields) != 1 || ce.Fields[0].Path != "/code" {
			t.Errorf("code %q: err = %+v, want request.invalid_input at /code", code, err)
		}
		noSecret(t, []string{code}, err)
	}
}

func TestUserActivateNotPending(t *testing.T) {
	e := newTestEnv(t)
	e.activeUser("alice")
	pv, err := e.plan(root, "core.user.activate", UserActivateInput{Username: "alice", Code: "918273"})
	if err != nil {
		t.Fatal(err)
	}
	want := []core.Guard{{ID: "user-pending", Message: "alice is already active"}}
	if !reflect.DeepEqual(pv.Guards, want) {
		t.Errorf("guards = %+v, want %+v", pv.Guards, want)
	}
	_, err = e.apply(root, pv.PlanID, "")
	wantCode(t, err, errcode.PlanGuardFailed)
	_, err = e.plan(root, "core.user.activate", UserActivateInput{Username: "nobody", Code: "918273"})
	wantCode(t, err, errcode.NotFound)
}

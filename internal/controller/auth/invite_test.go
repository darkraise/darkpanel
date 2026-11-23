package auth_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var inviteStart = time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

const invitePassword = "correct horse battery staple"

// inviteEnv is the core module's user actions over a real identity service.
type inviteEnv struct {
	t      *testing.T
	db     *store.DB
	clock  *coretest.FakeClock
	auth   *auth.Service
	engine *lifecycle.Engine
}

func newInviteEnv(t *testing.T) *inviteEnv {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{Side: store.SideController, BinaryVersion: "test",
		BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: inviteStart}, set, nil); err != nil {
		t.Fatal(err)
	}
	e := &inviteEnv{t: t, db: db, clock: coretest.NewFakeClock(inviteStart)}
	audit := &coretest.MemAudit{}
	e.auth = auth.New(db, coretest.Sealer{}, e.clock, audit,
		auth.Options{Params: auth.Params{Memory: 8, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}})
	reg, err := core.NewRegistry(coreactions.Module(coreactions.Deps{Auth: e.auth, Audit: audit}))
	if err != nil {
		t.Fatal(err)
	}
	e.engine, err = lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: audit,
		Redact: func(t reflect.Type, raw json.RawMessage) json.RawMessage {
			s, err := schema.Output(t)
			if err != nil {
				return json.RawMessage(`"` + secret.Redacted + `"`)
			}
			return secret.RedactJSON(raw, s)
		},
		Base: core.Env{DB: db, Sealer: coretest.Sealer{}, Clock: e.clock, Log: slog.New(slog.DiscardHandler)},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.engine.Close)
	return e
}

// active creates and activates username as an admin with invitePassword
// and returns its principal and TOTP secret.
func (e *inviteEnv) active(username string) (core.Principal, string) {
	e.t.Helper()
	ctx := context.Background()
	_, totp, err := e.auth.CreateUser(ctx, username, invitePassword)
	if err != nil {
		e.t.Fatal(err)
	}
	u, err := e.auth.ActivateUser(ctx, username, e.code(totp))
	if err != nil {
		e.t.Fatal(err)
	}
	e.clock.Advance(auth.TOTPPeriod)
	return core.Principal{Actor: "user:" + u.ID, UserID: u.ID, Scope: core.ScopeAdmin, Source: "127.0.0.1",
		Access: core.Access{Admin: true, Fleet: true}}, totp
}

func (e *inviteEnv) code(totp string) string {
	e.t.Helper()
	code, err := auth.TOTPCode(totp, e.clock.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	return code
}

func (e *inviteEnv) plan(p core.Principal, name, input string) (core.PlanView, error) {
	return e.engine.Plan(context.Background(), p, core.ActionRef{Name: name, Version: 1}, json.RawMessage(input), core.PlanOptions{})
}

// apply plans and applies name and fails the test unless it completes.
func (e *inviteEnv) apply(p core.Principal, name, input string) core.ChangeView {
	e.t.Helper()
	pv, err := e.plan(p, name, input)
	if err != nil {
		e.t.Fatalf("plan %s: %v", name, err)
	}
	cv, err := e.engine.Apply(context.Background(), p, pv.PlanID, core.ApplyOptions{})
	if err != nil || cv.State != core.StateCompleted {
		e.t.Fatalf("apply %s: %+v, %v", name, cv, err)
	}
	return cv
}

func wantErrCode(t *testing.T, what string, err error, code errcode.Code) {
	t.Helper()
	if got := errcode.From(err).Code; err == nil || got != code {
		t.Errorf("%s: err %v (code %q), want %s", what, err, got, code)
	}
}

func guardOK(t *testing.T, pv core.PlanView, id string) bool {
	t.Helper()
	for _, g := range pv.Guards {
		if g.ID == id {
			return g.OK
		}
	}
	t.Fatalf("plan has no guard %s: %+v", id, pv.Guards)
	return false
}

func TestInviteCreatesAnUnusablePendingUser(t *testing.T) {
	e := newInviteEnv(t)
	ctx := context.Background()
	admin, _ := e.active("root")
	if _, err := e.db.Writer().Exec(`INSERT INTO core_host_groups (id, name, created_at) VALUES ('g1', 'web', '2026-11-24T09:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}

	cv := e.apply(admin, "core.user.invite", `{"username":"mia","role":"member","grants":[{"target":"g1","role":"viewer"}],"email":"mia@example.com"}`)
	var out coreactions.Invitation
	if err := json.Unmarshal(cv.Output, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Token) != 43 || out.User.Username != "mia" || out.User.Role != auth.RoleMember || out.User.State != auth.StatePending ||
		!out.ExpiresAt.Equal(e.clock.Now().Add(auth.EnrollmentTTL)) {
		t.Fatalf("invite output %+v; want a pending member and a 72-hour token", out)
	}
	stored, err := e.engine.Change(ctx, cv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.Output), out.Token) || !strings.Contains(string(stored.Output), secret.Redacted) {
		t.Errorf("stored change output %s; want the token redacted", stored.Output)
	}
	var hash, tokenHash, email string
	if err := e.db.Read().QueryRow(`SELECT u.password_hash, e.token_hash, u.email FROM core_users u
		JOIN core_user_enrollments e ON e.user_id = u.id WHERE u.username = 'mia'`).Scan(&hash, &tokenHash, &email); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") || strings.Contains(tokenHash, out.Token) || email != "mia@example.com" {
		t.Errorf("stored hash %q, token hash %q, email %q; want an argon2id hash, a hashed token and the email", hash, tokenHash, email)
	}
	var target, role string
	if err := e.db.Read().QueryRow(`SELECT g.target, g.role FROM core_user_grants g JOIN core_users u ON u.id = g.user_id
		WHERE u.username = 'mia'`).Scan(&target, &role); err != nil || target != "g1" || role != auth.GrantViewer {
		t.Errorf("grant %q %q, %v; want viewer on g1", target, role, err)
	}

	for _, password := range []string{invitePassword, out.Token, ""} {
		_, err := e.auth.Login(ctx, auth.LoginRequest{Username: "mia", Password: password, Code: "123456", Source: "192.0.2.1"})
		wantErrCode(t, "password login of a pending invite", err, errcode.InvalidCredentials)
	}

	for name, input := range map[string]string{
		"unknown group":     `{"username":"nia","role":"member","grants":[{"target":"nope","role":"viewer"}]}`,
		"unknown role":      `{"username":"nia","role":"member","grants":[{"target":"*","role":"owner"}]}`,
		"twice":             `{"username":"nia","role":"member","grants":[{"target":"*","role":"viewer"},{"target":"*","role":"operator"}]}`,
		"admin with grants": `{"username":"nia","role":"admin","grants":[{"target":"*","role":"viewer"}]}`,
	} {
		_, err := e.plan(admin, "core.user.invite", input)
		wantErrCode(t, name, err, errcode.InvalidInput)
	}
	pv, err := e.plan(admin, "core.user.invite", `{"username":"mia","role":"member"}`)
	if err != nil {
		t.Fatal(err)
	}
	if guardOK(t, pv, "username-free") {
		t.Error("inviting a taken username: guard username-free OK")
	}

	renewed := e.apply(admin, "core.user.renew-invite", `{"username":"mia"}`)
	var again coreactions.Invitation
	if err := json.Unmarshal(renewed.Output, &again); err != nil {
		t.Fatal(err)
	}
	if again.Token == "" || again.Token == out.Token {
		t.Errorf("renew token %q; want a new token", again.Token)
	}
	e.apply(admin, "core.user.cancel-invite", `{"username":"mia"}`)
	if _, err := e.auth.GetUser(ctx, "mia"); errcode.From(err).Code != errcode.NotFound {
		t.Errorf("after cancel GetUser(mia) = %v; want not found", err)
	}
	for _, name := range []string{"core.user.renew-invite", "core.user.cancel-invite"} {
		_, err := e.plan(admin, name, `{"username":"root"}`)
		wantErrCode(t, name+" of an enrolled user", err, errcode.InvalidInput)
	}
}

func TestDisabledUserIsRefusedEverywhere(t *testing.T) {
	e := newInviteEnv(t)
	ctx := context.Background()
	root, _ := e.active("root")
	_, bobTOTP := e.active("bob")
	login, err := e.auth.Login(ctx, auth.LoginRequest{Username: "bob", Password: invitePassword, Code: e.code(bobTOTP), Source: "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(auth.TOTPPeriod)
	bob, err := e.auth.GetUser(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := e.auth.CreateToken(ctx, bob.ID, "ci", core.ScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}
	session := auth.Credentials{Session: login.SessionID, Source: "192.0.2.1"}
	bearer := auth.Credentials{Authorization: []string{"Bearer " + token}, Source: "192.0.2.1"}
	inv, err := e.auth.Invite(ctx, auth.InviteRequest{Username: "carl", Role: auth.RoleMember})
	if err != nil {
		t.Fatal(err)
	}
	carl, err := e.auth.BeginEnrollment(ctx, inv.Token, "192.0.2.1", false)
	if err != nil {
		t.Fatal(err)
	}

	cv := e.apply(root, "core.user.disable", `{"username":"bob"}`)
	if !strings.Contains(string(cv.Output), `"disabled":true`) {
		t.Errorf("disable output %s; want disabled", cv.Output)
	}
	e.apply(root, "core.user.disable", `{"username":"carl"}`)

	for name, c := range map[string]auth.Credentials{"session": session, "token": bearer} {
		_, err := e.auth.Authenticate(ctx, c)
		wantErrCode(t, "disabled user's "+name, err, errcode.AuthRequired)
	}
	_, err = e.auth.Login(ctx, auth.LoginRequest{Username: "bob", Password: invitePassword, Code: e.code(bobTOTP), Source: "192.0.2.1"})
	wantErrCode(t, "disabled user's password login", err, errcode.InvalidCredentials)
	_, _, err = e.auth.CreateToken(ctx, bob.ID, "more", core.ScopeRead, 0)
	wantErrCode(t, "disabled user's token creation", err, errcode.NotFound)
	_, err = e.auth.FinishEnrollment(ctx, auth.EnrollRequest{Token: inv.Token, Password: invitePassword,
		Code: e.code(carl.Secret), Source: "192.0.2.1"})
	wantErrCode(t, "disabled invite's enrollment", err, errcode.EnrollmentExpired)

	pv, err := e.plan(root, "core.user.delete", `{"username":"root"}`)
	if err != nil {
		t.Fatal(err)
	}
	if guardOK(t, pv, "not-last-admin") {
		t.Error("with the other admin disabled, deleting root: guard not-last-admin OK")
	}
	if pv, err = e.plan(root, "core.user.disable", `{"username":"root"}`); err != nil {
		t.Fatal(err)
	}
	if guardOK(t, pv, "not-last-admin") {
		t.Error("disabling the last active admin: guard not-last-admin OK")
	}
	if _, err := e.auth.SetDisabled(ctx, "root", true); errcode.From(err).Code != errcode.InvalidInput {
		t.Errorf("SetDisabled(root) = %v; want the last active admin refused", err)
	}

	e.apply(root, "core.user.enable", `{"username":"bob"}`)
	for name, c := range map[string]auth.Credentials{"session": session, "token": bearer} {
		if _, err := e.auth.Authenticate(ctx, c); err != nil {
			t.Errorf("enabled user's %s: %v", name, err)
		}
	}
	if pv, err = e.plan(root, "core.user.delete", `{"username":"root"}`); err != nil {
		t.Fatal(err)
	}
	if !guardOK(t, pv, "not-last-admin") {
		t.Error("with bob enabled again, deleting root: guard not-last-admin not OK")
	}
	if u, _ := e.auth.GetUser(ctx, "bob"); u.Disabled {
		t.Error("bob still disabled after core.user.enable")
	}
}

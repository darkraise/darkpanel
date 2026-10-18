package auth

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

const testPassword = "correct horse battery"

// newTestService returns a service over a new migrated controller database,
// with a fake clock reading t0 and an in-memory audit log.
func newTestService(t *testing.T) (*Service, *coretest.FakeClock, *coretest.MemAudit) {
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
	opts := store.MigrateOptions{Side: store.SideController, BinaryVersion: "test",
		BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: t0}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	clock := coretest.NewFakeClock(t0)
	audit := &coretest.MemAudit{}
	return New(db, coretest.Sealer{}, clock, audit, Options{Params: testParams}), clock, audit
}

// createActive creates and activates username with testPassword and returns
// the user and its TOTP secret. It then advances the clock by one TOTP period,
// so the next code is not the step activation consumed.
func createActive(t *testing.T, s *Service, clock *coretest.FakeClock, username string) (User, string) {
	t.Helper()
	ctx := context.Background()
	_, secret, err := s.CreateUser(ctx, username, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	code, err := TOTPCode(secret, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.ActivateUser(ctx, username, code)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(TOTPPeriod)
	return u, secret
}

// count returns the number of rows of table matching where (with args).
func count(t *testing.T, s *Service, table, where string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.Read().QueryRow("SELECT count(*) FROM "+table+" WHERE "+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// insertSession adds a raw session row for userID, standing in for a login.
func insertSession(t *testing.T, s *Service, userID, idHash string) {
	t.Helper()
	now := formatTime(s.clock.Now())
	if _, err := s.db.Writer().Exec(`INSERT INTO core_sessions
		(id_hash, user_id, created_at, last_seen_at, expires_at, source_ip, user_agent) VALUES (?, ?, ?, ?, ?, '', '')`,
		idHash, userID, now, now, now); err != nil {
		t.Fatal(err)
	}
}

// insertToken adds a raw token row for userID.
func insertToken(t *testing.T, s *Service, userID, id string) {
	t.Helper()
	now := formatTime(s.clock.Now())
	if _, err := s.db.Writer().Exec(`INSERT INTO core_tokens
		(id, user_id, name, scope, secret_hash, created_at, expires_at) VALUES (?, ?, 'ci', 'read', '', ?, ?)`,
		id, userID, now, now); err != nil {
		t.Fatal(err)
	}
}

// wantCode fails unless err carries code.
func wantCode(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func TestCreateUser(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()
	u, secret, err := s.CreateUser(ctx, "alice", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == "" || u.Username != "alice" || u.Role != RoleAdmin || u.State != StatePending || !u.CreatedAt.Equal(t0) {
		t.Errorf("user = %+v, want a pending admin created at %s", u, t0)
	}
	key, err := b32.DecodeString(secret)
	if err != nil || len(key) != TOTPSecretSize {
		t.Fatalf("secret %q: %v, want %d bytes of unpadded base32", secret, err, TOTPSecretSize)
	}
	var hash string
	var ct []byte
	if err := s.db.Read().QueryRow(`SELECT password_hash, totp_secret_ct FROM core_users WHERE id = ?`, u.ID).
		Scan(&hash, &ct); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.hasher.verify(ctx, hash, testPassword); err != nil || !ok {
		t.Errorf("stored hash does not verify the password: %v", err)
	}
	if opened, err := s.sealer.Open("core_users.totp_secret_ct."+u.ID, ct); err != nil || string(opened) != string(key) {
		t.Errorf("stored TOTP secret does not open with the row's associated data: %v", err)
	}
	if _, err := s.sealer.Open("core_users.totp_secret_ct.other", ct); err == nil {
		t.Error("stored TOTP secret opens with another row's associated data")
	}
	if strings.Contains(string(ct), string(key)) {
		t.Error("TOTP secret stored in clear")
	}
}

func TestCreateUserRefusals(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()
	if _, _, err := s.CreateUser(ctx, "alice", testPassword); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, username, password, path string
	}{
		{"duplicate", "alice", testPassword, "/username"},
		{"bad username", "Alice", testPassword, "/username"},
		{"short password", "bob", "short pass", "/password"},
	} {
		_, _, err := s.CreateUser(ctx, tt.username, tt.password)
		if err == nil {
			t.Errorf("%s: created", tt.name)
			continue
		}
		wantField(t, err, tt.path)
		if strings.Contains(err.Error(), tt.password) {
			t.Errorf("%s: error %q quotes the password", tt.name, err)
		}
	}
	if n := count(t, s, "core_users", "1 = 1"); n != 1 {
		t.Errorf("%d users, want 1", n)
	}
}

func TestActivateUser(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	_, secret, err := s.CreateUser(ctx, "alice", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := TOTPCode(secret, clock.Now())
	_, err = s.ActivateUser(ctx, "alice", wrongCode(t, secret, clock))
	wantField(t, err, "/code")
	if _, err := s.ActivateUser(ctx, "nobody", code); err == nil {
		t.Error("activated an unknown user")
	} else {
		wantCode(t, err, errcode.NotFound)
	}
	u, err := s.ActivateUser(ctx, "alice", code)
	if err != nil {
		t.Fatal(err)
	}
	if u.State != StateActive {
		t.Errorf("state = %s, want active", u.State)
	}
	if n := count(t, s, "core_users", "id = ? AND totp_last_step = ?", u.ID, totpStep(clock.Now())); n != 1 {
		t.Error("activation did not store the accepted step")
	}
	_, err = s.ActivateUser(ctx, "alice", code)
	wantField(t, err, "/username")
}

func TestResetTOTP(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, secret := createActive(t, s, clock, "alice")
	insertSession(t, s, u.ID, "h1")
	insertToken(t, s, u.ID, "01TOKEN")
	got, fresh, err := s.ResetTOTP(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StatePending || fresh == secret {
		t.Errorf("after reset: state %s, new secret differs %v; want pending and a new secret", got.State, fresh != secret)
	}
	if count(t, s, "core_sessions", "user_id = ?", u.ID) != 0 {
		t.Error("reset-totp kept the user's sessions")
	}
	if count(t, s, "core_tokens", "user_id = ?", u.ID) != 1 {
		t.Error("reset-totp removed the user's tokens")
	}
	code, _ := TOTPCode(fresh, clock.Now())
	if _, err := s.ActivateUser(ctx, "alice", code); err != nil {
		t.Errorf("activate with the new secret: %v", err)
	}
	_, _, err = s.ResetTOTP(ctx, "nobody")
	wantCode(t, err, errcode.NotFound)
}

func TestResetPassword(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	insertSession(t, s, u.ID, "h1")
	const next = "a different long password"
	if _, err := s.ResetPassword(ctx, "alice", next); err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := s.db.Read().QueryRow(`SELECT password_hash FROM core_users WHERE id = ?`, u.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.hasher.verify(ctx, hash, next); !ok {
		t.Error("the new password does not verify")
	}
	if count(t, s, "core_sessions", "user_id = ?", u.ID) != 0 {
		t.Error("reset-password kept the user's sessions")
	}
	_, err := s.ResetPassword(ctx, "alice", "too short")
	wantField(t, err, "/password")
	_, err = s.ResetPassword(ctx, "nobody", next)
	wantCode(t, err, errcode.NotFound)
}

func TestDeleteUser(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	alice, _ := createActive(t, s, clock, "alice")
	if _, _, err := s.CreateUser(ctx, "pending", testPassword); err != nil {
		t.Fatal(err)
	}
	err := s.DeleteUser(ctx, "alice")
	wantField(t, err, "/username")
	if err := s.DeleteUser(ctx, "pending"); err != nil {
		t.Errorf("delete a pending user beside the last admin: %v", err)
	}
	bob, _ := createActive(t, s, clock, "bob")
	insertSession(t, s, bob.ID, "h1")
	insertToken(t, s, bob.ID, "01TOKEN")
	if err := s.DeleteUser(ctx, "bob"); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "core_sessions", "user_id = ?", bob.ID)+count(t, s, "core_tokens", "user_id = ?", bob.ID) != 0 {
		t.Error("delete kept the user's sessions or tokens")
	}
	if _, err := s.GetUser(ctx, "bob"); err == nil {
		t.Error("deleted user still found")
	}
	wantCode(t, s.DeleteUser(ctx, "bob"), errcode.NotFound)
	if _, err := s.GetUser(ctx, "alice"); err != nil || alice.ID == "" {
		t.Errorf("alice: %v", err)
	}
}

func TestListAndGetUsers(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	empty, err := s.ListUsers(ctx)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("ListUsers on no users = %v, %v; want an empty non-nil slice", empty, err)
	}
	bob, _ := createActive(t, s, clock, "bob")
	alice, _, err := s.CreateUser(ctx, "alice", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	users, err := s.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].ID != alice.ID || users[1].ID != bob.ID {
		t.Fatalf("ListUsers = %+v, want alice then bob", users)
	}
	if users[1].State != StateActive || users[0].State != StatePending {
		t.Errorf("states = %s, %s; want pending, active", users[0].State, users[1].State)
	}
	got, err := s.GetUser(ctx, "bob")
	if err != nil || got.ID != bob.ID || !got.CreatedAt.Equal(bob.CreatedAt) {
		t.Errorf("GetUser(bob) = %+v, %v; want %+v", got, err, bob)
	}
	_, err = s.GetUser(ctx, "nobody")
	wantCode(t, err, errcode.NotFound)
	_, err = s.GetUser(ctx, "Not Valid")
	wantCode(t, err, errcode.NotFound)
}

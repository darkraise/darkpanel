package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/secret"
)

// newSession creates a session for userID as a login would.
func newSession(t *testing.T, s *Service, userID string) (string, time.Time) {
	t.Helper()
	var id string
	var exp time.Time
	err := s.db.Write(context.Background(), func(tx *sql.Tx) error {
		var err error
		id, exp, err = s.createSession(context.Background(), tx, userID, "10.0.0.1", "test-agent")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id, exp
}

func lastSeen(t *testing.T, s *Service, id string) time.Time {
	t.Helper()
	raw, ok := decodeSessionID(id)
	if !ok {
		t.Fatalf("session id %q does not decode", id)
	}
	var at string
	if err := s.db.Read().QueryRow(`SELECT last_seen_at FROM core_sessions WHERE id_hash = ?`, sessionHash(raw)).Scan(&at); err != nil {
		t.Fatal(err)
	}
	v, err := parseTime(at)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSessionStoresOnlyItsHash(t *testing.T) {
	s, clock, _ := newTestService(t)
	u, _ := createActive(t, s, clock, "alice")
	id, exp := newSession(t, s, u.ID)
	raw, ok := decodeSessionID(id)
	if !ok || len(raw) != 32 {
		t.Fatalf("session id %q is not 32 bytes of unpadded base64url", id)
	}
	if want := clock.Now().Add(SessionIdle); !exp.Equal(want) {
		t.Errorf("expiry = %s, want %s", exp, want)
	}
	sum := sha256.Sum256(raw)
	if count(t, s, "core_sessions", "id_hash = ? AND user_id = ? AND source_ip = '10.0.0.1' AND user_agent = 'test-agent'",
		hex.EncodeToString(sum[:]), u.ID) != 1 {
		t.Fatal("no session row keyed by the hex SHA-256 of the id")
	}
	if count(t, s, "core_sessions", "id_hash = ?", id) != 0 {
		t.Error("the raw session id is stored")
	}
	other, _ := newSession(t, s, u.ID)
	if other == id {
		t.Error("two sessions share an id")
	}
}

func TestAuthenticateSession(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	id, _ := newSession(t, s, u.ID)
	start := clock.Now()
	clock.Advance(30 * time.Minute)
	got, err := s.authenticateSession(ctx, id, "10.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	want := core.Principal{Actor: "user:" + u.ID, UserID: u.ID, Scope: core.ScopeAdmin, Source: "10.0.0.2"}
	if got.Principal != want || !got.FromSession || got.Token != nil || got.User.ID != u.ID {
		t.Errorf("identity = %+v, want principal %+v from a session", got, want)
	}
	if exp := clock.Now().Add(SessionIdle); !got.ExpiresAt.Equal(exp) {
		t.Errorf("expiry = %s, want %s (idle limit from this request)", got.ExpiresAt, exp)
	}
	for range 11 {
		clock.Advance(59 * time.Minute)
		if _, err := s.authenticateSession(ctx, id, "10.0.0.2"); err != nil {
			t.Fatalf("active session refused at %s: %v", clock.Now().Sub(start), err)
		}
	}
	got, err = s.authenticateSession(ctx, id, "10.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	if abs := start.Add(SessionLifetime); !got.ExpiresAt.Equal(abs) {
		t.Errorf("expiry near the end = %s, want the absolute limit %s", got.ExpiresAt, abs)
	}
	clock.Advance(start.Add(SessionLifetime).Sub(clock.Now()))
	_, err = s.authenticateSession(ctx, id, "10.0.0.2")
	wantCode(t, err, errcode.AuthRequired)
}

func TestSessionIdleExpiry(t *testing.T) {
	s, clock, _ := newTestService(t)
	u, _ := createActive(t, s, clock, "alice")
	id, _ := newSession(t, s, u.ID)
	clock.Advance(SessionIdle - time.Second)
	if _, err := s.authenticateSession(context.Background(), id, ""); err != nil {
		t.Fatalf("refused one second before the idle limit: %v", err)
	}
	clock.Advance(SessionIdle)
	_, err := s.authenticateSession(context.Background(), id, "")
	wantCode(t, err, errcode.AuthRequired)
}

func TestLastSeenWrittenAtMostOnceAMinute(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	id, _ := newSession(t, s, u.ID)
	created := clock.Now()
	clock.Advance(LastSeenInterval - time.Second)
	if _, err := s.authenticateSession(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	if got := lastSeen(t, s, id); !got.Equal(created) {
		t.Errorf("last_seen_at = %s after %s, want unchanged %s", got, LastSeenInterval-time.Second, created)
	}
	clock.Advance(time.Second)
	if _, err := s.authenticateSession(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	if got := lastSeen(t, s, id); !got.Equal(clock.Now()) {
		t.Errorf("last_seen_at = %s after %s, want %s", got, LastSeenInterval, clock.Now())
	}
}

func TestAuthenticateSessionRefusals(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	id, _ := newSession(t, s, u.ID)
	unknown, _ := newSessionID()
	for _, bad := range []string{"", "short", id + "x", strings.ToUpper(id), unknown} {
		_, err := s.authenticateSession(ctx, bad, "")
		wantCode(t, err, errcode.AuthRequired)
	}
	if _, err := s.db.Writer().Exec(`UPDATE core_users SET state = 'pending' WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}
	_, err := s.authenticateSession(ctx, id, "")
	wantCode(t, err, errcode.AuthRequired)
}

func TestEndSession(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	id, _ := newSession(t, s, u.ID)
	keep, _ := newSession(t, s, u.ID)
	userID, ok, err := s.endSession(ctx, id)
	if err != nil || !ok || userID != u.ID {
		t.Fatalf("endSession = %q, %v, %v; want %s, true", userID, ok, err, u.ID)
	}
	if _, err := s.authenticateSession(ctx, id, ""); err == nil {
		t.Error("ended session still authenticates")
	}
	if _, err := s.authenticateSession(ctx, keep, ""); err != nil {
		t.Errorf("another session of the user ended: %v", err)
	}
	if _, ok, err := s.endSession(ctx, id); err != nil || ok {
		t.Errorf("ending an ended session = %v, %v; want false, nil", ok, err)
	}
	if _, ok, err := s.endSession(ctx, "malformed"); err != nil || ok {
		t.Errorf("ending a malformed id = %v, %v; want false, nil", ok, err)
	}
}

func TestCSRF(t *testing.T) {
	s, clock, _ := newTestService(t)
	u, _ := createActive(t, s, clock, "alice")
	id, _ := newSession(t, s, u.ID)
	other, _ := newSession(t, s, u.ID)
	token := s.CSRFToken(id)
	raw, _ := decodeSessionID(id)
	m := hmac.New(sha256.New, coretest.Sealer{}.Derive(secret.CSRFLabel))
	m.Write(raw)
	if want := b64url.EncodeToString(m.Sum(nil)); token != want {
		t.Fatalf("CSRF token = %q, want HMAC-SHA256 of the session id under the derived key %q", token, want)
	}
	if s.CSRFToken(id) != token {
		t.Error("CSRF token is not deterministic")
	}
	if !s.VerifyCSRF(id, token) {
		t.Error("the session's own token is refused")
	}
	tampered := []byte(token)
	tampered[0] ^= 1
	for name, tt := range map[string][2]string{
		"another session's token": {id, s.CSRFToken(other)},
		"tampered token":          {id, string(tampered)},
		"empty token":             {id, ""},
		"malformed session id":    {"malformed", s.CSRFToken("malformed")},
	} {
		if s.VerifyCSRF(tt[0], tt[1]) {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestTruncate(t *testing.T) {
	long := strings.Repeat("a", maxUserAgent-1) + "é"
	got := truncate(long, maxUserAgent)
	if got != strings.Repeat("a", maxUserAgent-1) {
		t.Errorf("truncate split a rune: %d bytes", len(got))
	}
	if truncate("short", maxUserAgent) != "short" {
		t.Error("truncate changed a short value")
	}
}

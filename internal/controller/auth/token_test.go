package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var tokenPattern = regexp.MustCompile(`^dpt_[0-9A-HJKMNP-TV-Z]{26}_[A-Z2-7]{52}$`)

const day = 24 * time.Hour

func TestCreateToken(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	info, token, err := s.CreateToken(ctx, u.ID, "ci", core.ScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !tokenPattern.MatchString(token) {
		t.Fatalf("token %q does not match dpt_<ULID>_<52 base32 characters>", token)
	}
	if !strings.HasPrefix(token, TokenPrefix+info.ID+"_") {
		t.Errorf("token %q does not carry the id %s", token, info.ID)
	}
	now := clock.Now()
	if info.Name != "ci" || info.Scope != core.ScopeRead || !info.CreatedAt.Equal(now) ||
		!info.ExpiresAt.Equal(now.Add(DefaultTokenDays*day)) || info.LastUsedAt != nil || info.RevokedAt != nil {
		t.Errorf("info = %+v, want a read token expiring in %d days", info, DefaultTokenDays)
	}
	raw, err := b32.DecodeString(token[len(TokenPrefix)+27:])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if count(t, s, "core_tokens", "id = ? AND secret_hash = ?", info.ID, hex.EncodeToString(sum[:])) != 1 {
		t.Error("the stored hash is not the hex SHA-256 of the secret")
	}
	if count(t, s, "core_tokens", "secret_hash LIKE ?", "%"+token[len(TokenPrefix)+27:]+"%") != 0 {
		t.Error("the secret is stored")
	}
	year, _, err := s.CreateToken(ctx, u.ID, "year", core.ScopeAdmin, MaxTokenDays)
	if err != nil || !year.ExpiresAt.Equal(now.Add(MaxTokenDays*day)) {
		t.Errorf("365-day token = %+v, %v", year, err)
	}
}

func TestCreateTokenRefusals(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	for _, tt := range []struct {
		name  string
		scope core.Scope
		days  int
		path  string
	}{
		{"ci", core.ScopeRead, MaxTokenDays + 1, "/expiresInDays"},
		{"ci", core.ScopeRead, -1, "/expiresInDays"},
		{"ci", "root", 1, "/scope"},
		{"", core.ScopeRead, 1, "/name"},
		{strings.Repeat("n", 65), core.ScopeRead, 1, "/name"},
	} {
		_, _, err := s.CreateToken(ctx, u.ID, tt.name, tt.scope, tt.days)
		wantField(t, err, tt.path)
	}
	_, _, err := s.CreateToken(ctx, "01NOSUCHUSER", "ci", core.ScopeRead, 1)
	wantCode(t, err, errcode.NotFound)
	if n := count(t, s, "core_tokens", "1 = 1"); n != 0 {
		t.Errorf("%d tokens created by refused calls", n)
	}
}

func TestAuthenticateToken(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	info, token, err := s.CreateToken(ctx, u.ID, "ci", core.ScopeOperate, 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.authenticateToken(ctx, token, "10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	want := core.Principal{Actor: "token:" + info.ID, UserID: u.ID, TokenID: info.ID, Scope: core.ScopeOperate, Source: "10.0.0.5"}
	if got.Principal != want || got.FromSession || got.Token == nil || got.Token.ID != info.ID ||
		got.User.ID != u.ID || !got.ExpiresAt.Equal(info.ExpiresAt) {
		t.Errorf("identity = %+v, want principal %+v from a token", got, want)
	}
	used := clock.Now()
	if got.Token.LastUsedAt == nil || !got.Token.LastUsedAt.Equal(used) {
		t.Errorf("lastUsedAt = %v, want %s", got.Token.LastUsedAt, used)
	}
	clock.Advance(LastUsedInterval - time.Second)
	if got, _ := s.GetToken(ctx, u.ID, info.ID); got.LastUsedAt == nil || !got.LastUsedAt.Equal(used) {
		t.Fatalf("lastUsedAt = %v, want %s", got.LastUsedAt, used)
	}
	if _, err := s.authenticateToken(ctx, token, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetToken(ctx, u.ID, info.ID); !got.LastUsedAt.Equal(used) {
		t.Errorf("lastUsedAt moved within a minute: %v", got.LastUsedAt)
	}
	clock.Advance(time.Second)
	if _, err := s.authenticateToken(ctx, token, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetToken(ctx, u.ID, info.ID); !got.LastUsedAt.Equal(clock.Now()) {
		t.Errorf("lastUsedAt = %v after a minute, want %s", got.LastUsedAt, clock.Now())
	}

	mid := len(token) - 10
	flipped := token[:mid] + map[bool]string{true: "B", false: "A"}[token[mid] == 'A'] + token[mid+1:]
	// The last character carries one bit of the secret and four spare bits;
	// setting a spare bit decodes to the same bytes.
	spare := token[:len(token)-1] + string(token[len(token)-1]+1)
	other, _, err := s.CreateToken(ctx, u.ID, "other", core.ScopeRead, 1)
	if err != nil {
		t.Fatal(err)
	}
	swapped := TokenPrefix + other.ID + token[len(TokenPrefix)+26:]
	for _, bad := range []string{"", "dpt_", flipped, spare, swapped, strings.TrimPrefix(token, TokenPrefix),
		"dpx_" + token[4:], token + "A", strings.ToLower(token), token[:len(token)-1]} {
		_, err := s.authenticateToken(ctx, bad, "")
		wantCode(t, err, errcode.AuthRequired)
	}
	clock.Advance(day)
	_, err = s.authenticateToken(ctx, token, "")
	wantCode(t, err, errcode.AuthRequired)
}

func TestTokenOfInactiveOrRevoked(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	createActive(t, s, clock, "bob")
	revoked, revokedToken, _ := s.CreateToken(ctx, u.ID, "a", core.ScopeRead, 1)
	_, pendingToken, _ := s.CreateToken(ctx, u.ID, "b", core.ScopeRead, 1)
	if _, err := s.RevokeToken(ctx, u.ID, revoked.ID); err != nil {
		t.Fatal(err)
	}
	_, err := s.authenticateToken(ctx, revokedToken, "")
	wantCode(t, err, errcode.AuthRequired)
	if _, _, err := s.ResetTOTP(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	_, err = s.authenticateToken(ctx, pendingToken, "")
	wantCode(t, err, errcode.AuthRequired)
	if err := s.DeleteUser(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	_, err = s.authenticateToken(ctx, pendingToken, "")
	wantCode(t, err, errcode.AuthRequired)
}

func TestListGetRevokeTokens(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	alice, _ := createActive(t, s, clock, "alice")
	bob, _ := createActive(t, s, clock, "bob")
	empty, err := s.ListTokens(ctx, alice.ID)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("ListTokens with none = %v, %v; want an empty non-nil slice", empty, err)
	}
	first, _, _ := s.CreateToken(ctx, alice.ID, "first", core.ScopeRead, 1)
	second, _, _ := s.CreateToken(ctx, alice.ID, "second", core.ScopeRead, 1)
	bobs, _, _ := s.CreateToken(ctx, bob.ID, "bob", core.ScopeRead, 1)
	list, err := s.ListTokens(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("ListTokens = %+v, want alice's two tokens newest first", list)
	}
	_, err = s.GetToken(ctx, alice.ID, bobs.ID)
	wantCode(t, err, errcode.NotFound)
	_, err = s.RevokeToken(ctx, alice.ID, bobs.ID)
	wantCode(t, err, errcode.NotFound)
	got, err := s.RevokeToken(ctx, alice.ID, first.ID)
	if err != nil || got.RevokedAt == nil || !got.RevokedAt.Equal(clock.Now()) {
		t.Fatalf("RevokeToken = %+v, %v; want revokedAt %s", got, err, clock.Now())
	}
	at := clock.Now()
	clock.Advance(time.Hour)
	again, err := s.RevokeToken(ctx, alice.ID, first.ID)
	if err != nil || !again.RevokedAt.Equal(at) {
		t.Errorf("second revoke = %+v, %v; want the first revocation time kept", again, err)
	}
	list, _ = s.ListTokens(ctx, alice.ID)
	if len(list) != 2 || list[1].RevokedAt == nil {
		t.Errorf("a revoked token is missing from the list or not marked: %+v", list)
	}
}

func TestPurgeSessions(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	idle, _ := newSession(t, s, u.ID)
	old, _ := newSession(t, s, u.ID)
	for range 15 {
		clock.Advance(50 * time.Minute)
		if _, err := s.authenticateSession(ctx, old, ""); err != nil && clock.Now().Sub(t0) < SessionLifetime {
			t.Fatal(err)
		}
	}
	fresh, _ := newSession(t, s, u.ID)
	if err := s.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"idle": idle, "past its lifetime": old} {
		raw, _ := decodeSessionID(id)
		if count(t, s, "core_sessions", "id_hash = ?", sessionHash(raw)) != 0 {
			t.Errorf("%s session survived the purge", name)
		}
	}
	raw, _ := decodeSessionID(fresh)
	if count(t, s, "core_sessions", "id_hash = ?", sessionHash(raw)) != 1 {
		t.Error("a live session was purged")
	}
}

func TestPurgeTokens(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	expired, _, _ := s.CreateToken(ctx, u.ID, "expired", core.ScopeRead, 1)
	recent, _, _ := s.CreateToken(ctx, u.ID, "recently expired", core.ScopeRead, 3)
	revoked, _, _ := s.CreateToken(ctx, u.ID, "revoked", core.ScopeRead, 90)
	live, _, _ := s.CreateToken(ctx, u.ID, "live", core.ScopeRead, 90)
	if _, err := s.RevokeToken(ctx, u.ID, revoked.ID); err != nil {
		t.Fatal(err)
	}
	clock.Advance(31 * day)
	if err := s.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		info TokenInfo
		kept bool
	}{{expired, false}, {recent, true}, {revoked, false}, {live, true}} {
		if got := count(t, s, "core_tokens", "id = ?", tt.info.ID) == 1; got != tt.kept {
			t.Errorf("token %q kept = %v, want %v", tt.info.Name, got, tt.kept)
		}
	}
}
